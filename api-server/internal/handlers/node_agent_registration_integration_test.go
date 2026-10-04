package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func seedPendingRegistrationNode(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	token := crypto.GenerateRandomString(32)
	var nodeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, reg_token, api_key, ip, port, status, role)
		 VALUES ('pending', $1, 'pending', '0.0.0.0'::inet, 443, 'pending', 'exit')
		 RETURNING id::text`,
		token,
	).Scan(&nodeID); err != nil {
		t.Fatalf("seed pending node: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID); err != nil {
			t.Errorf("clean up pending node: %v", err)
		}
	})
	return nodeID, token
}

func waitForBlockedQuery(t *testing.T, pool *pgxpool.Pool, fragments ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var queries []string
		rows, err := pool.Query(ctx,
			`SELECT query FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		)
		if err != nil {
			t.Fatalf("inspect blocked PostgreSQL queries: %v", err)
		}
		for rows.Next() {
			var query string
			if err := rows.Scan(&query); err != nil {
				rows.Close()
				t.Fatalf("scan blocked PostgreSQL query: %v", err)
			}
			queries = append(queries, query)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("read blocked PostgreSQL queries: %v", err)
		}
		for _, query := range queries {
			matches := true
			for _, fragment := range fragments {
				matches = matches && strings.Contains(query, fragment)
			}
			if matches {
				return
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("timed out waiting for blocked query containing %q; blocked queries: %q", fragments, queries)
		}
	}
}

func registrationRequest(app *fiber.App, token string, port int) (int, error) {
	body, err := json.Marshal(registerNodeRequest{
		RegToken: token, IP: "203.0.113.91", Port: port, XrayVersion: "test",
		Name: "registered-node", Country: "JP", Region: "tokyo",
	})
	if err != nil {
		return 0, fmt.Errorf("marshal registration request: %w", err)
	}
	request := httptest.NewRequest(fiber.MethodPost, "/nodes/register", bytes.NewReader(body))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request, 5_000)
	if err != nil {
		return 0, fmt.Errorf("register node: %w", err)
	}
	status := response.StatusCode
	if err := response.Body.Close(); err != nil {
		return 0, fmt.Errorf("close registration response: %w", err)
	}
	return status, nil
}

func TestRegisterConsumesTokenOnceAndCreatesOneAttachedDirectChainThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	nodeID, token := seedPendingRegistrationNode(t, pool)
	groupID := seedChainGroup(t, pool, "registration-group-"+crypto.NewUUID())
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`,
		groupID, nodeID,
	); err != nil {
		t.Fatalf("seed group membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, exit_node_id, exit_port, transport)
		 VALUES ('pending-direct', NULL, $1, 443, 'tcp_udp')`,
		nodeID,
	); err != nil {
		t.Fatalf("seed existing direct chain: %v", err)
	}

	app := fiber.New()
	app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{}).Register)
	t.Cleanup(func() { _ = app.Shutdown() })

	start := make(chan struct{})
	type result struct {
		status int
		err    error
	}
	results := make(chan result, 2)
	var requests sync.WaitGroup
	requests.Add(2)
	for range 2 {
		go func() {
			defer requests.Done()
			<-start
			status, err := registrationRequest(app, token, 8443)
			results <- result{status: status, err: err}
		}()
	}
	close(start)
	requests.Wait()
	close(results)

	gotStatuses := make([]int, 0, 2)
	for requestResult := range results {
		if requestResult.err != nil {
			t.Fatalf("concurrent registration request: %v", requestResult.err)
		}
		gotStatuses = append(gotStatuses, requestResult.status)
	}
	sort.Ints(gotStatuses)
	wantStatuses := []int{fiber.StatusOK, fiber.StatusUnauthorized}
	if len(gotStatuses) != len(wantStatuses) || gotStatuses[0] != wantStatuses[0] || gotStatuses[1] != wantStatuses[1] {
		t.Fatalf("concurrent registration statuses = %v, want %v", gotStatuses, wantStatuses)
	}
	status, err := registrationRequest(app, token, 9443)
	if err != nil {
		t.Fatalf("repeat registration request: %v", err)
	}
	if status != fiber.StatusUnauthorized {
		t.Errorf("repeated registration status = %d, want 401", status)
	}

	var directChains, directPort, attachments int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(MAX(exit_port), 0)
		 FROM node_chains WHERE exit_node_id = $1 AND relay_pool_id IS NULL`,
		nodeID,
	).Scan(&directChains, &directPort); err != nil {
		t.Fatalf("count direct chains: %v", err)
	}
	if directChains != 1 {
		t.Errorf("direct chain count = %d, want 1", directChains)
	}
	if directPort != 8443 {
		t.Errorf("direct chain port = %d, want 8443", directPort)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*)
		 FROM node_group_chains ngc
		 JOIN node_chains c ON c.id = ngc.chain_id
		 WHERE ngc.node_group_id = $1 AND c.exit_node_id = $2 AND c.relay_pool_id IS NULL`,
		groupID, nodeID,
	).Scan(&attachments); err != nil {
		t.Fatalf("count direct chain attachments: %v", err)
	}
	if attachments != 1 {
		t.Errorf("direct chain attachment count = %d, want 1", attachments)
	}
}

func TestRegisterRollsBackConsumedTokenWhenDirectChainCreationFailsThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	nodeID, token := seedPendingRegistrationNode(t, pool)

	app := fiber.New()
	app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{}).Register)
	t.Cleanup(func() { _ = app.Shutdown() })

	statusCode, err := registrationRequest(app, token, 70000)
	if err != nil {
		t.Fatalf("failed registration request: %v", err)
	}
	if statusCode != fiber.StatusInternalServerError {
		t.Fatalf("registration status = %d, want 500", statusCode)
	}

	var status, storedToken string
	if err := pool.QueryRow(ctx,
		`SELECT status, reg_token FROM nodes WHERE id = $1`, nodeID,
	).Scan(&status, &storedToken); err != nil {
		t.Fatalf("read node after failed registration: %v", err)
	}
	if status != "pending" || storedToken != token {
		t.Errorf("node after failed registration = status %q token %q, want pending with original token", status, storedToken)
	}

	var chains int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_chains WHERE exit_node_id = $1 AND relay_pool_id IS NULL`, nodeID,
	).Scan(&chains); err != nil {
		t.Fatalf("count chains after failed registration: %v", err)
	}
	if chains != 0 {
		t.Errorf("direct chain count after failed registration = %d, want 0", chains)
	}
}

func TestRegisterAndSetNodesRemovalCommitConsistentVisibilityThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	nodeID, token := seedPendingRegistrationNode(t, pool)
	groupID := seedChainGroup(t, pool, "registration-removal-group-"+crypto.NewUUID())
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, groupID, nodeID,
	); err != nil {
		t.Fatalf("seed group membership: %v", err)
	}
	var chainID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, exit_node_id, exit_port, transport)
		 VALUES ('pending-direct', NULL, $1, 443, 'tcp_udp') RETURNING id::text`, nodeID,
	).Scan(&chainID); err != nil {
		t.Fatalf("seed direct chain: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin chain blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(ctx) })
	var lockedChainID string
	if err := blocker.QueryRow(ctx,
		`SELECT id::text FROM node_chains WHERE id = $1 FOR UPDATE`, chainID,
	).Scan(&lockedChainID); err != nil {
		t.Fatalf("lock direct chain: %v", err)
	}

	app := fiber.New()
	app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{}).Register)
	app.Put("/admin/node-groups/:id/nodes", NewAdminNodeGroupHandler(pool).SetNodes)
	t.Cleanup(func() { _ = app.Shutdown() })
	type requestResult struct {
		status int
		err    error
	}
	registrationResult := make(chan requestResult, 1)
	go func() {
		status, err := registrationRequest(app, token, 8443)
		registrationResult <- requestResult{status: status, err: err}
	}()
	waitForBlockedQuery(t, pool, "FROM node_chains", "FOR UPDATE")

	removalResult := make(chan requestResult, 1)
	go func() {
		request := httptest.NewRequest(
			fiber.MethodPut,
			"/admin/node-groups/"+groupID+"/nodes",
			bytes.NewBufferString(`{"node_ids":[]}`),
		)
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := app.Test(request)
		if err != nil {
			removalResult <- requestResult{err: err}
			return
		}
		status := response.StatusCode
		err = response.Body.Close()
		removalResult <- requestResult{status: status, err: err}
	}()
	waitForBlockedQuery(t, pool, "node_groups", "FOR UPDATE")

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release chain blocker: %v", err)
	}
	registered := <-registrationResult
	removed := <-removalResult
	if registered.err != nil || registered.status != fiber.StatusOK {
		t.Fatalf("registration result = status %d error %v, want 200", registered.status, registered.err)
	}
	if removed.err != nil || removed.status != fiber.StatusOK {
		t.Fatalf("membership removal result = status %d error %v, want 200", removed.status, removed.err)
	}

	var memberships, attachments int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_group_nodes WHERE node_group_id = $1 AND node_id = $2`, groupID, nodeID,
	).Scan(&memberships); err != nil {
		t.Fatalf("count final memberships: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_group_chains WHERE node_group_id = $1 AND chain_id = $2`, groupID, chainID,
	).Scan(&attachments); err != nil {
		t.Fatalf("count final attachments: %v", err)
	}
	if memberships != 0 || attachments != 0 {
		t.Errorf("final membership/attachment = %d/%d, want 0/0", memberships, attachments)
	}
}
