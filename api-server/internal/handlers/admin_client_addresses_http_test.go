package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

// Fleet-wide completeness must not depend on nodes left by other integration
// tests. Migrate a uniquely named database, never truncate the shared fixture.
func clientAddressesHTTPTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for client address HTTP tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := "client_addresses_http_" + strings.ReplaceAll(crypto.NewUUID(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("create isolated fixture database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+quoted); err != nil {
			t.Errorf("drop isolated fixture database: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate isolated fixture: %v", err)
	}
	return pool
}

func TestAdminClientAddressesFromAuthenticatedHeartbeats(t *testing.T) {
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("TEST_REDIS_ADDR required for client address HTTP tests")
	}
	pool := clientAddressesHTTPTestDB(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("connect test Redis: %v", err)
	}

	groupID, planID, userID := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	entryID, exitID := crypto.NewUUID(), crypto.NewUUID()
	credential, secondCredential := crypto.NewUUID(), crypto.NewUUID()
	// Every Redis write uses these fresh account/node IDs. Never flush the DB.
	t.Cleanup(func() {
		keys := []string{
			"account:" + userID + ":client_addresses",
			"account:" + userID + ":client_address_starts",
			"node:" + entryID + ":relay_flows",
			"node:" + entryID + ":tunnels",
			"node:" + exitID + ":relay_flows",
			"node:" + exitID + ":tunnels",
		}
		if err := rdb.Del(context.Background(), keys...).Err(); err != nil {
			t.Errorf("clean fixture Redis keys: %v", err)
		}
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed fixture: %v", err)
		}
	}
	exec(`INSERT INTO node_groups (id, name) VALUES ($1, $2)`, groupID, groupID)
	exec(`INSERT INTO plans (id, name, duration_days, max_devices, max_concurrent, node_group_id)
		VALUES ($1, 'client address HTTP test', 30, 5, 1, $2)`, planID, groupID)
	exec(`INSERT INTO users (id, email, password_hash, sub_token, plan_id, status)
		VALUES ($1, $2, '', $3, $4, 'active')`, userID, userID+"@example.test", crypto.NewUUID(), planID)
	for _, uuid := range []string{credential, secondCredential} {
		exec(`INSERT INTO devices (user_id, xray_uuid) VALUES ($1, $2)`, userID, uuid)
	}
	const entryIP, exitIP = "203.0.113.81", "203.0.113.82"
	for _, node := range []struct{ id, role, ip string }{
		{entryID, "relay", entryIP}, {exitID, "exit", exitIP},
	} {
		exec(`INSERT INTO nodes (id, name, api_key, ip, port, role, status)
			VALUES ($1::uuid, $1::text, 'test', $2::inet, 8443, $3, 'offline')`, node.id, node.ip, node.role)
	}

	app := fiber.New()
	t.Cleanup(func() { _ = app.Shutdown() })
	agent := NewNodeAgentHandler(pool, rdb, services.ManagedEntryDNSIntentConfig{})
	admin := NewAdminClientAddressHandler(pool, rdb)
	// The real authentication middleware supplies node_id; exercise the same
	// trusted local contract without requiring registration credentials here.
	app.Post("/nodes/:id/heartbeat", func(c *fiber.Ctx) error {
		c.Locals("node_id", c.Params("id"))
		return c.Next()
	}, agent.Heartbeat)
	app.Get("/admin/users/:id/client-addresses", admin.Get)
	heartbeat := func(nodeID string, payload heartbeatRequest) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(fiber.MethodPost, "/nodes/"+nodeID+"/heartbeat", bytes.NewReader(body))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		resp, err := app.Test(req, 10_000)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != fiber.StatusOK {
			t.Fatalf("heartbeat: status=%d body=%s error=%v", resp.StatusCode, data, err)
		}
	}
	type observationResponse struct {
		Complete         bool     `json:"complete"`
		Addresses        []string `json:"addresses"`
		Count            *int     `json:"count"`
		Limit            int      `json:"limit"`
		OverLimit        bool     `json:"over_limit"`
		CandidateAddress string   `json:"candidate_address"`
		SelectionReason  string   `json:"selection_reason"`
		Enforced         bool     `json:"enforced"`
	}
	observe := func() observationResponse {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/admin/users/"+userID+"/client-addresses", nil), 10_000)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != fiber.StatusOK {
			t.Fatalf("admin observation: status=%d body=%s error=%v", resp.StatusCode, data, err)
		}
		var got observationResponse
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("decode observation: %v: %s", err, data)
		}
		return got
	}
	const olderIP, newerIP = "198.51.100.11", "198.51.100.12"
	start := time.Now().Add(-time.Minute).UnixMilli()
	connection := func(uuid, peer string, port int, acceptedAt int64) nodeprov.TunnelConnection {
		return nodeprov.TunnelConnection{UUID: uuid, PeerIP: peer, PeerPort: port,
			LocalIP: exitIP, LocalPort: 8443, AcceptedAt: acceptedAt}
	}
	flows := []nodeprov.RelayFlow{
		{PeerIP: entryIP, PeerPort: 41001, ExitIP: exitIP, ExitPort: 8443, ClientIP: olderIP},
		{PeerIP: entryIP, PeerPort: 41002, ExitIP: exitIP, ExitPort: 8443, ClientIP: newerIP},
	}
	publish := func(connections []nodeprov.TunnelConnection) {
		t.Helper()
		heartbeat(entryID, heartbeatRequest{RelayFlows: &nodeprov.RelayFlowReport{Known: true, Flows: flows}})
		heartbeat(exitID, heartbeatRequest{TunnelReport: &nodeprov.TunnelReport{Known: true, Connections: connections}})
	}

	t.Run("two original addresses sharing one credential select newest without enforcement", func(t *testing.T) {
		publish([]nodeprov.TunnelConnection{
			connection(credential, entryIP, 41001, start),
			connection(credential, entryIP, 41002, start+1000),
		})
		got := observe()
		if !got.Complete || got.Count == nil || *got.Count != 2 ||
			!reflect.DeepEqual(got.Addresses, []string{olderIP, newerIP}) || got.Limit != 1 ||
			!got.OverLimit || got.CandidateAddress != newerIP || got.Enforced || got.SelectionReason != "" {
			t.Fatalf("want original source addresses, newest candidate, observation only; got %+v", got)
		}
	})

	t.Run("duplicate transports and direct plus relayed traffic count one address account wide", func(t *testing.T) {
		publish([]nodeprov.TunnelConnection{
			connection(credential, entryIP, 41001, start),
			connection(credential, entryIP, 41001, start),
			connection(credential, olderIP, 42001, start+2000),
			connection(secondCredential, olderIP, 42002, start+3000),
			// A transport for another account must not increase this count.
			connection(crypto.NewUUID(), "198.51.100.99", 42003, start+4000),
		})
		got := observe()
		if !got.Complete || got.Count == nil || *got.Count != 1 ||
			!reflect.DeepEqual(got.Addresses, []string{olderIP}) || got.OverLimit ||
			got.CandidateAddress != "" || got.Enforced {
			t.Fatalf("want one deduplicated account-wide address; got %+v", got)
		}
	})

	t.Run("unknown Exit report is null not zero and invalidates old evidence", func(t *testing.T) {
		heartbeat(exitID, heartbeatRequest{TunnelReport: &nodeprov.TunnelReport{Known: false}})
		got := observe()
		if got.Complete || got.Count != nil || got.OverLimit || got.CandidateAddress != "" ||
			got.Enforced || got.SelectionReason != "incomplete_observation" {
			t.Fatalf("unknown report must suspend counting and selection; got %+v", got)
		}
	})

	t.Run("unknown Entry report cannot turn the relay into a client", func(t *testing.T) {
		publish([]nodeprov.TunnelConnection{connection(credential, entryIP, 41001, start)})
		heartbeat(entryID, heartbeatRequest{RelayFlows: &nodeprov.RelayFlowReport{Known: false}})
		got := observe()
		if got.Complete || got.Count != nil || len(got.Addresses) != 0 || got.OverLimit ||
			got.CandidateAddress != "" || got.Enforced || got.SelectionReason != "incomplete_observation" {
			t.Fatalf("unknown attribution must not count the relay address; got %+v", got)
		}
	})
}
