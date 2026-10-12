package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// The heartbeat records the agent's installed policy and the route list turns
// it into a per-route state. This drives both endpoints through HTTP with the
// fingerprint an agent would compute from the rules it fetched.
func TestRouteApplyFollowsHeartbeatPolicyReports(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()

	var entryID, exitID string
	for _, n := range []struct {
		id         *string
		name, role string
		ip         string
	}{{&entryID, "apply-entry-" + suffix, "relay", "203.0.113.71"}, {&exitID, "apply-exit-" + suffix, "exit", "203.0.113.72"}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO nodes (name, api_key, ip, port, role, status) VALUES ($1, 'test', $2::inet, 8443, $3, 'online') RETURNING id::text`,
			n.name, n.ip, n.role,
		).Scan(n.id); err != nil {
			t.Fatalf("seed %s: %v", n.name, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_chains WHERE entry_node_id = $1`, entryID)
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = ANY($1::uuid[])`, []string{entryID, exitID})
	})
	var chainID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_chains (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 'entry.example.test', 24991, $3, 8443, 'tcp') RETURNING id::text`,
		"apply-chain-"+suffix, entryID, exitID,
	).Scan(&chainID); err != nil {
		t.Fatalf("seed chain: %v", err)
	}

	agent := NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{})
	admin := NewAdminNodeChainHandler(pool)
	app := fiber.New()
	app.Post("/nodes/:id/heartbeat", func(c *fiber.Ctx) error { c.Locals("node_id", c.Params("id")); return c.Next() }, agent.Heartbeat)
	app.Get("/admin/node-chains", admin.List)
	t.Cleanup(func() { _ = app.Shutdown() })

	heartbeat := func(nodeID string, body map[string]any) {
		t.Helper()
		payload, _ := json.Marshal(body)
		request := httptest.NewRequest(fiber.MethodPost, "/nodes/"+nodeID+"/heartbeat", bytes.NewReader(payload))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := app.Test(request, 10_000)
		if err != nil || response.StatusCode != fiber.StatusOK {
			t.Fatalf("heartbeat %s: %v %v", nodeID, err, response)
		}
	}
	applyState := func() services.RouteApplyStatus {
		t.Helper()
		response, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/admin/node-chains", nil), 10_000)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, _ := io.ReadAll(response.Body)
		var chains []nodeChainResponse
		if err := json.Unmarshal(body, &chains); err != nil {
			t.Fatalf("decode chains: %v: %s", err, body)
		}
		for _, ch := range chains {
			if ch.ID == chainID {
				if ch.Apply == nil {
					t.Fatal("route has no apply status")
				}
				return *ch.Apply
			}
		}
		t.Fatalf("chain %s missing from list", chainID)
		return services.RouteApplyStatus{}
	}

	// Agents that predate reporting send nothing: unknown, not failed.
	if got := applyState(); got.State != services.RouteApplyUnknown {
		t.Fatalf("before any report: %+v, want unknown", got)
	}

	entryFP := nodeprov.PolicyFingerprint(nodeprov.RoleRelay,
		[]nodeprov.RelayRule{{EntryPort: 24991, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.72", ExitPort: 8443}}, nil)
	exitFP := nodeprov.PolicyFingerprint(nodeprov.RoleExit, nil,
		[]nodeprov.ExitRule{{ExitPort: 8443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.71"}}})
	heartbeat(entryID, map[string]any{"policy_fingerprint": entryFP})
	if got := applyState(); got.State != services.RouteApplyUnknown {
		t.Fatalf("only the Entry reported: %+v, want unknown (Exit never reported)", got)
	}
	heartbeat(exitID, map[string]any{"policy_fingerprint": exitFP})
	if got := applyState(); got.State != services.RouteApplyApplied {
		t.Fatalf("both nodes on the current policy: %+v, want applied", got)
	}

	// Changing the route makes the reported policy stale until the agents catch up.
	if _, err := pool.Exec(ctx, `UPDATE node_chains SET transport = 'tcp_udp' WHERE id = $1`, chainID); err != nil {
		t.Fatal(err)
	}
	if got := applyState(); got.State != services.RouteApplyPending {
		t.Fatalf("after editing the route: %+v, want pending", got)
	}

	// A failed reconciliation on the Entry is surfaced with its error.
	heartbeat(entryID, map[string]any{"policy_fingerprint": entryFP, "policy_error": "nft -f: refused"})
	if got := applyState(); got.State != services.RouteApplyFailed || got.Error != "nft -f: refused" {
		t.Fatalf("after a failed apply: %+v, want failed with the nft error", got)
	}

	// An agent heartbeat without policy fields keeps the last report.
	heartbeat(entryID, map[string]any{"cpu_usage": 1})
	if got := applyState(); got.State != services.RouteApplyFailed {
		t.Fatalf("legacy heartbeat erased the last report: %+v", got)
	}

	// A disabled route is intentionally absent from every policy.
	if _, err := pool.Exec(ctx, `UPDATE node_chains SET enabled = false WHERE id = $1`, chainID); err != nil {
		t.Fatal(err)
	}
	if got := applyState(); got.State != services.RouteApplyNotUsed {
		t.Fatalf("disabled route: %+v, want not_applicable", got)
	}
}
