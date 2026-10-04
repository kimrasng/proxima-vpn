package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

type nodeChainHTTPFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	app  *fiber.App
}

func newNodeChainHTTPFixture(t *testing.T) nodeChainHTTPFixture {
	t.Helper()
	pool := chainTestDB(t)
	app := fiber.New()
	handler := NewAdminNodeChainHandler(pool)
	app.Post("/admin/node-chains", handler.Create)
	app.Post("/admin/node-chains/batch", handler.Batch)
	plans := NewAdminPlanHandler(pool)
	app.Get("/admin/plans/:id/routes", plans.GetRoutes)
	app.Put("/admin/plans/:id/routes", plans.SetRoutes)
	app.Patch("/admin/node-chains/:id", handler.Update)
	app.Delete("/admin/node-chains/:id", handler.Delete)
	app.Put("/admin/node-chains/:id/groups", handler.SetGroups)
	t.Cleanup(func() {
		if err := app.Shutdown(); err != nil {
			t.Errorf("shut down Fiber app: %v", err)
		}
	})
	return nodeChainHTTPFixture{t: t, pool: pool, app: app}
}

func (f nodeChainHTTPFixture) request(method, path, body string) (int, []byte) {
	f.t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := f.app.Test(request)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			f.t.Errorf("close %s %s response: %v", method, path, err)
		}
	}()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return response.StatusCode, responseBody
}

func (f nodeChainHTTPFixture) addNodeToGroup(groupID, nodeID string) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`,
		groupID, nodeID,
	); err != nil {
		f.t.Fatalf("add node to chain group: %v", err)
	}
}

func createChainBody(t *testing.T, request createNodeChainRequest) string {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal create chain request: %v", err)
	}
	return string(payload)
}

func TestCreateNodeChainReturnsCreatedExplicitLinkThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-create-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, fixture.pool, "chain-create-relay-"+suffix, "relay", 443)
	seedChainManagedHostname(t, fixture, relayID, "relay.example.test")

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "created-" + suffix, EntryNodeID: &relayID, EntryHost: "relay.example.test",
		ExitNodeID: exitID, ExitPort: 443, Transport: "tcp", Priority: 7,
	}))
	if status != fiber.StatusCreated {
		t.Fatalf("create chain status = %d, want 201; body=%s", status, body)
	}
	var created nodeChainResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created chain: %v", err)
	}
	if created.EntryNodeID == nil || *created.EntryNodeID != relayID || created.EntryNodeName == nil || created.EntryPort == nil {
		t.Errorf("created chain = %+v, want explicit entry node %s with allocated entry port", created, relayID)
	}
}

func TestCreateNodeChainRejectsLegacyRelayPoolThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-bad-pool-exit-"+suffix, "exit", 443)
	poolID := seedChainGroup(t, fixture.pool, "chain-bad-pool-"+suffix)

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "rejected-" + suffix, RelayPoolID: &poolID, EntryHost: "relay.example.test",
		ExitNodeID: exitID, ExitPort: 443, Transport: "tcp", Priority: 7,
	}))
	if status != fiber.StatusBadRequest {
		t.Fatalf("bad relay pool status = %d, want 400; body=%s", status, body)
	}
	if string(body) != `{"error":"relay_pool_id is no longer accepted; use entry_node_id"}` {
		t.Errorf("bad relay pool body = %s", body)
	}
}

func TestCreateNodeChainRejectsEntryNodeWithoutRelayRoleThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-empty-pool-exit-"+suffix, "exit", 443)
	entryID := seedChainNode(t, fixture.pool, "chain-entry-role-"+suffix, "exit", 443)

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "bad-entry-" + suffix, EntryNodeID: &entryID,
		ExitNodeID: exitID, ExitPort: 443, Transport: "tcp",
	}))

	if status != fiber.StatusBadRequest {
		t.Fatalf("empty relay pool status = %d, want 400; body=%s", status, body)
	}
}

func TestCreateNodeChainRejectsUnregisteredEntryThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "registered-exit-"+suffix, "exit", 443)
	entryID := seedChainNode(t, fixture.pool, "pending-entry-"+suffix, "relay", 443)
	if _, err := fixture.pool.Exec(context.Background(),
		`UPDATE nodes SET status = 'pending', ip = '0.0.0.0'::inet WHERE id = $1`, entryID,
	); err != nil {
		t.Fatalf("set entry pending: %v", err)
	}
	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "unregistered-" + suffix, EntryNodeID: &entryID, EntryHost: "relay.example.test",
		ExitNodeID: exitID, ExitPort: 443, Transport: "tcp",
	}))
	if status != fiber.StatusBadRequest {
		t.Fatalf("pending entry status = %d, want 400; body=%s", status, body)
	}
}

func TestCreateNodeChainRejectsSameEntryAndExitThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-blank-host-exit-"+suffix, "exit", 443)

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "same-node-" + suffix, EntryNodeID: &exitID,
		ExitNodeID: exitID, ExitPort: 443, Transport: "tcp",
	}))

	if status != fiber.StatusBadRequest {
		t.Fatalf("same entry and exit status = %d, want 400; body=%s", status, body)
	}
}

func TestUpdateNodeChainRejectsBlankRelayedEntryHostThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-update-host-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, fixture.pool, "chain-update-host-relay-"+suffix, "relay", 443)
	poolID := seedChainGroup(t, fixture.pool, "chain-update-host-pool-"+suffix)
	fixture.addNodeToGroup(poolID, relayID)
	var chainID string
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO node_chains
		   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 'relay.example.test', 29991, $3, 443, 'tcp') RETURNING id::text`,
		"chain-update-host-"+suffix, poolID, exitID,
	).Scan(&chainID); err != nil {
		t.Fatalf("seed relayed chain: %v", err)
	}

	status, body := fixture.request(fiber.MethodPatch, "/admin/node-chains/"+chainID, `{"entry_host":"   "}`)

	if status != fiber.StatusBadRequest {
		t.Fatalf("blank updated entry host status = %d, want 400; body=%s", status, body)
	}
	var entryHost string
	if err := fixture.pool.QueryRow(ctx, `SELECT entry_host FROM node_chains WHERE id = $1`, chainID).Scan(&entryHost); err != nil {
		t.Fatalf("read entry host after rejected update: %v", err)
	}
	if entryHost != "relay.example.test" {
		t.Errorf("entry host after rejected update = %q, want original value", entryHost)
	}
}

func TestUpdateNodeChainRejectsInvalidReferencedRelayPoolThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-update-pool-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, fixture.pool, "chain-update-pool-relay-"+suffix, "relay", 443)
	poolID := seedChainGroup(t, fixture.pool, "chain-update-pool-"+suffix)
	fixture.addNodeToGroup(poolID, relayID)
	var chainID string
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO node_chains
		   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 'relay.example.test', 29994, $3, 443, 'tcp') RETURNING id::text`,
		"chain-update-pool-"+suffix, poolID, exitID,
	).Scan(&chainID); err != nil {
		t.Fatalf("seed relayed chain: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE nodes SET role = 'exit' WHERE id = $1`, relayID); err != nil {
		t.Fatalf("corrupt relay pool member role: %v", err)
	}

	status, body := fixture.request(fiber.MethodPatch, "/admin/node-chains/"+chainID, `{"priority":8}`)

	if status != fiber.StatusConflict {
		t.Fatalf("update with invalid referenced pool status = %d, want 409; body=%s", status, body)
	}
}

func TestCreateNodeChainSerializesWithRelayRoleDemotionThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-role-race-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, fixture.pool, "chain-role-race-relay-"+suffix, "relay", 443)
	seedChainManagedHostname(t, fixture, relayID, "relay.example.test")

	blocker, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin relay blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(ctx) })
	var lockedNodeID string
	if err := blocker.QueryRow(ctx,
		`SELECT id::text FROM nodes WHERE id = $1 FOR UPDATE`, relayID,
	).Scan(&lockedNodeID); err != nil {
		t.Fatalf("lock relay node: %v", err)
	}

	type response struct {
		status int
		body   []byte
		err    error
	}
	createBody := createChainBody(t, createNodeChainRequest{
		Name: "chain-role-race-" + suffix, EntryNodeID: &relayID, EntryHost: "relay.example.test",
		ExitNodeID: exitID, ExitPort: 443, Transport: "tcp",
	})
	createResult := make(chan response, 1)
	go func() {
		request := httptest.NewRequest(fiber.MethodPost, "/admin/node-chains", bytes.NewBufferString(createBody))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		result, err := fixture.app.Test(request)
		if err != nil {
			createResult <- response{err: err}
			return
		}
		body, readErr := io.ReadAll(result.Body)
		closeErr := result.Body.Close()
		if readErr != nil {
			createResult <- response{err: readErr}
			return
		}
		createResult <- response{status: result.StatusCode, body: body, err: closeErr}
	}()
	waitForBlockedQuery(t, fixture.pool, "FROM nodes", "FOR UPDATE")

	roleApp := fiber.New()
	roleApp.Put("/admin/nodes/:id", NewAdminNodeHandler(fixture.pool, nil, "").UpdateNode)
	t.Cleanup(func() { _ = roleApp.Shutdown() })
	roleResult := make(chan response, 1)
	go func() {
		request := httptest.NewRequest(fiber.MethodPut, "/admin/nodes/"+relayID, bytes.NewBufferString(`{"role":"exit"}`))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		result, err := roleApp.Test(request)
		if err != nil {
			roleResult <- response{err: err}
			return
		}
		body, readErr := io.ReadAll(result.Body)
		closeErr := result.Body.Close()
		if readErr != nil {
			roleResult <- response{err: readErr}
			return
		}
		roleResult <- response{status: result.StatusCode, body: body, err: closeErr}
	}()
	waitForBlockedQuery(t, fixture.pool, "SELECT status, port, firewall_preset, role FROM nodes", "FOR UPDATE")

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release relay blocker: %v", err)
	}
	created := <-createResult
	demoted := <-roleResult
	if created.err != nil || created.status != fiber.StatusCreated {
		t.Fatalf("concurrent chain create status = %d error %v, want 201; body=%s", created.status, created.err, created.body)
	}
	if demoted.err != nil || demoted.status != fiber.StatusConflict {
		t.Fatalf("concurrent relay demotion status = %d error %v, want 409; body=%s", demoted.status, demoted.err, demoted.body)
	}
	var role string
	if err := fixture.pool.QueryRow(ctx, `SELECT role FROM nodes WHERE id = $1`, relayID).Scan(&role); err != nil {
		t.Fatalf("read relay role after race: %v", err)
	}
	if role != "relay" {
		t.Errorf("relay role after race = %q, want relay", role)
	}
}

func TestDeleteNodeChainRejectsDirectAndDeletesExplicitAndLegacyThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-delete-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, fixture.pool, "chain-delete-relay-"+suffix, "relay", 443)
	poolID := seedChainGroup(t, fixture.pool, "chain-delete-pool-"+suffix)
	fixture.addNodeToGroup(poolID, relayID)
	var directID, explicitID, relayedID string
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO node_chains
		   (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, '203.0.113.1', 29993, $3, 443, 'tcp') RETURNING id::text`,
		"explicit-"+suffix, relayID, exitID,
	).Scan(&explicitID); err != nil {
		t.Fatalf("seed explicit chain: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 443, 'tcp_udp') RETURNING id::text`, "direct-"+suffix, exitID,
	).Scan(&directID); err != nil {
		t.Fatalf("seed direct chain: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO node_chains
		   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 'relay.example.test', 29992, $3, 443, 'tcp') RETURNING id::text`,
		"relayed-"+suffix, poolID, exitID,
	).Scan(&relayedID); err != nil {
		t.Fatalf("seed relayed chain: %v", err)
	}

	directStatus, directBody := fixture.request(fiber.MethodDelete, "/admin/node-chains/"+directID, "")
	explicitStatus, explicitBody := fixture.request(fiber.MethodDelete, "/admin/node-chains/"+explicitID, "")
	relayedStatus, relayedBody := fixture.request(fiber.MethodDelete, "/admin/node-chains/"+relayedID, "")

	if directStatus != fiber.StatusConflict {
		t.Fatalf("direct delete status = %d, want 409; body=%s", directStatus, directBody)
	}
	if relayedStatus != fiber.StatusNoContent {
		t.Fatalf("relayed delete status = %d, want 204; body=%s", relayedStatus, relayedBody)
	}
	if explicitStatus != fiber.StatusNoContent {
		t.Fatalf("explicit delete status = %d, want 204; body=%s", explicitStatus, explicitBody)
	}
}

func TestCreateNodeChainReturnsSafeConflictForOverlappingPortThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-overlap-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, fixture.pool, "chain-overlap-relay-"+suffix, "relay", 443)
	seedChainManagedHostname(t, fixture, relayID, "relay.example.test")
	poolID := seedChainGroup(t, fixture.pool, "chain-overlap-pool-"+suffix)
	fixture.addNodeToGroup(poolID, relayID)

	var entryPort int
	if err := fixture.pool.QueryRow(ctx,
		`SELECT candidate FROM generate_series(30000, 60000) AS candidate
		 WHERE NOT EXISTS (SELECT 1 FROM node_chains WHERE entry_port = candidate)
		 ORDER BY candidate LIMIT 1`,
	).Scan(&entryPort); err != nil {
		t.Fatalf("select free entry port: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO node_chains
		   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 'existing.example.test', $3, $4, 443, 'tcp')`,
		"existing-"+suffix, poolID, entryPort, exitID,
	); err != nil {
		t.Fatalf("seed overlapping chain: %v", err)
	}

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "overlap-" + suffix, EntryNodeID: &relayID, EntryHost: "relay.example.test",
		EntryPort: entryPort, ExitNodeID: exitID, ExitPort: 443, Transport: "tcp", Priority: 7,
	}))
	if status != fiber.StatusConflict {
		t.Fatalf("overlapping entry port status = %d, want 409; body=%s", status, body)
	}
	if string(body) != `{"error":"entry port overlaps an existing chain"}` {
		t.Errorf("overlapping entry port body = %s, want stable client-safe error", body)
	}
}

func TestCreateNodeChainRejectsMalformedExitIDWithSafeErrorThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	payload, err := json.Marshal(createNodeChainRequest{ExitNodeID: "not-a-uuid"})
	if err != nil {
		t.Fatalf("marshal create request: %v", err)
	}

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", string(payload))
	if status != fiber.StatusBadRequest {
		t.Fatalf("malformed exit ID status = %d, want 400; body=%s", status, body)
	}
	if string(body) != `{"error":"exit_node_id must be a UUID"}` {
		t.Errorf("malformed exit ID body = %s, want stable client-safe error", body)
	}
}

func TestSetGroupsRejectsInvalidIDsWithoutChangingVisibilityThroughHTTP(t *testing.T) {
	testCases := []struct {
		name     string
		groupIDs func(string, string) []string
	}{
		{name: "missing", groupIDs: func(valid, missing string) []string { return []string{valid, missing} }},
		{name: "duplicate", groupIDs: func(valid, _ string) []string { return []string{valid, valid} }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newNodeChainHTTPFixture(t)
			ctx := context.Background()
			suffix := crypto.NewUUID()
			exitID := seedChainNode(t, fixture.pool, "set-groups-exit-"+suffix, "exit", 443)
			oldGroupID := seedChainGroup(t, fixture.pool, "set-groups-old-"+suffix)
			newGroupID := seedChainGroup(t, fixture.pool, "set-groups-new-"+suffix)
			var chainID string
			if err := fixture.pool.QueryRow(ctx,
				`INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
				 VALUES ($1, $2, 443, 'tcp') RETURNING id::text`,
				"set-groups-chain-"+suffix, exitID,
			).Scan(&chainID); err != nil {
				t.Fatalf("seed chain: %v", err)
			}
			if _, err := fixture.pool.Exec(ctx,
				`INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1, $2)`,
				oldGroupID, chainID,
			); err != nil {
				t.Fatalf("seed chain visibility: %v", err)
			}

			groupIDs := testCase.groupIDs(newGroupID, crypto.NewUUID())
			payload, err := json.Marshal(setChainGroupsRequest{GroupIDs: &groupIDs})
			if err != nil {
				t.Fatalf("marshal set groups request: %v", err)
			}
			status, body := fixture.request(fiber.MethodPut, "/admin/node-chains/"+chainID+"/groups", string(payload))
			if status != fiber.StatusBadRequest {
				t.Fatalf("invalid group IDs status = %d, want 400; body=%s", status, body)
			}
			if string(body) != `{"error":"group_ids contains an unknown or duplicate group"}` {
				t.Errorf("invalid group IDs body = %s", body)
			}

			var oldAttachments, newAttachments int
			if err := fixture.pool.QueryRow(ctx,
				`SELECT COUNT(*) FILTER (WHERE node_group_id = $1),
				        COUNT(*) FILTER (WHERE node_group_id = $2)
				 FROM node_group_chains WHERE chain_id = $3`,
				oldGroupID, newGroupID, chainID,
			).Scan(&oldAttachments, &newAttachments); err != nil {
				t.Fatalf("read chain visibility: %v", err)
			}
			if oldAttachments != 1 || newAttachments != 0 {
				t.Errorf("visibility after rejected request = old:%d new:%d, want old:1 new:0", oldAttachments, newAttachments)
			}
		})
	}
}

func TestSetGroupsRequiresPresentArrayAndExplicitEmptyClearsVisibilityThroughHTTP(t *testing.T) {
	for _, body := range []string{`{}`, `{"group_ids":null}`} {
		t.Run(body, func(t *testing.T) {
			fixture := newNodeChainHTTPFixture(t)
			ctx := context.Background()
			suffix := crypto.NewUUID()
			exitID := seedChainNode(t, fixture.pool, "set-groups-required-exit-"+suffix, "exit", 443)
			groupID := seedChainGroup(t, fixture.pool, "set-groups-required-"+suffix)
			var chainID string
			if err := fixture.pool.QueryRow(ctx,
				`INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
				 VALUES ($1, $2, 443, 'tcp') RETURNING id::text`, "set-groups-required-"+suffix, exitID,
			).Scan(&chainID); err != nil {
				t.Fatalf("seed chain: %v", err)
			}
			if _, err := fixture.pool.Exec(ctx,
				`INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1, $2)`, groupID, chainID,
			); err != nil {
				t.Fatalf("seed visibility: %v", err)
			}

			status, responseBody := fixture.request(fiber.MethodPut, "/admin/node-chains/"+chainID+"/groups", body)

			if status != fiber.StatusBadRequest {
				t.Fatalf("missing group_ids status = %d, want 400; body=%s", status, responseBody)
			}
			var attachments int
			if err := fixture.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM node_group_chains WHERE chain_id = $1`, chainID,
			).Scan(&attachments); err != nil {
				t.Fatalf("count visibility: %v", err)
			}
			if attachments != 1 {
				t.Errorf("attachments after rejected request = %d, want 1", attachments)
			}
		})
	}

	fixture := newNodeChainHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "set-groups-clear-exit-"+suffix, "exit", 443)
	groupID := seedChainGroup(t, fixture.pool, "set-groups-clear-"+suffix)
	var chainID string
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 443, 'tcp') RETURNING id::text`, "set-groups-clear-"+suffix, exitID,
	).Scan(&chainID); err != nil {
		t.Fatalf("seed clearable chain: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1, $2)`, groupID, chainID,
	); err != nil {
		t.Fatalf("seed clearable visibility: %v", err)
	}

	status, body := fixture.request(fiber.MethodPut, "/admin/node-chains/"+chainID+"/groups", `{"group_ids":[]}`)

	if status != fiber.StatusOK {
		t.Fatalf("clear group_ids status = %d, want 200; body=%s", status, body)
	}
	var attachments int
	if err := fixture.pool.QueryRow(ctx, `SELECT COUNT(*) FROM node_group_chains WHERE chain_id = $1`, chainID).Scan(&attachments); err != nil {
		t.Fatalf("count cleared visibility: %v", err)
	}
	if attachments != 0 {
		t.Errorf("attachments after explicit clear = %d, want 0", attachments)
	}
}
