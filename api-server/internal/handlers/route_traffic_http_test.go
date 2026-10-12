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
)

// An Entry's per-route counter deltas, sent in its heartbeat, must be summed
// into the route that owns that entry port and protocol on that Entry - and
// only that route - and appear on the route list.
func TestRouteTrafficFromHeartbeatsAppearsOnTheRouteList(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()

	var entryID, otherEntryID, exitID, poolMemberID, groupID string
	for _, n := range []struct {
		id         *string
		name, role string
		ip         string
	}{
		{&entryID, "traffic-entry-" + suffix, "relay", "203.0.113.81"},
		{&otherEntryID, "traffic-other-" + suffix, "relay", "203.0.113.82"},
		{&exitID, "traffic-exit-" + suffix, "exit", "203.0.113.83"},
		{&poolMemberID, "traffic-pool-" + suffix, "relay", "203.0.113.84"},
	} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO nodes (name, api_key, ip, port, role, status) VALUES ($1, 'test', $2::inet, 8443, $3, 'online') RETURNING id::text`,
			n.name, n.ip, n.role,
		).Scan(n.id); err != nil {
			t.Fatalf("seed %s: %v", n.name, err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, "traffic-pool-"+suffix).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, groupID, poolMemberID); err != nil {
		t.Fatal(err)
	}
	nodes := []string{entryID, otherEntryID, exitID, poolMemberID}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_chains WHERE exit_node_id = $1`, exitID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, groupID)
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = ANY($1::uuid[])`, nodes)
	})

	chain := func(name, entryCol, entryValue string, port int, transport string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO node_chains (name, `+entryCol+`, entry_host, entry_port, exit_node_id, exit_port, transport)
			 VALUES ($1, $2, 'entry.example.test', $3, $4, 8443, $5) RETURNING id::text`,
			name+"-"+suffix, entryValue, port, exitID, transport,
		).Scan(&id); err != nil {
			t.Fatalf("seed chain %s: %v", name, err)
		}
		return id
	}
	both := chain("traffic-both", "entry_node_id", entryID, 24981, "tcp_udp")
	tcpOnly := chain("traffic-tcp", "entry_node_id", entryID, 24982, "tcp")
	other := chain("traffic-other", "entry_node_id", otherEntryID, 24983, "tcp")
	legacy := chain("traffic-legacy", "relay_pool_id", groupID, 24984, "udp")

	agent := NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{})
	admin := NewAdminNodeChainHandler(pool)
	app := fiber.New()
	app.Post("/nodes/:id/heartbeat", func(c *fiber.Ctx) error { c.Locals("node_id", c.Params("id")); return c.Next() }, agent.Heartbeat)
	app.Get("/admin/node-chains", admin.List)
	t.Cleanup(func() { _ = app.Shutdown() })

	heartbeat := func(nodeID string, traffic []map[string]any) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"route_traffic": traffic})
		request := httptest.NewRequest(fiber.MethodPost, "/nodes/"+nodeID+"/heartbeat", bytes.NewReader(payload))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := app.Test(request, 10_000)
		if err != nil || response.StatusCode != fiber.StatusOK {
			t.Fatalf("heartbeat %s: %v %v", nodeID, err, response)
		}
	}
	listed := func() map[string]nodeChainResponse {
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
		out := map[string]nodeChainResponse{}
		for _, ch := range chains {
			out[ch.ID] = ch
		}
		return out
	}
	traffic := func(chains map[string]nodeChainResponse, id string) services.RouteTrafficSummary {
		t.Helper()
		if chains[id].Traffic == nil {
			t.Fatalf("relayed route %s has no traffic summary", id)
		}
		return *chains[id].Traffic
	}

	if got := traffic(listed(), both); got != (services.RouteTrafficSummary{}) {
		t.Fatalf("route without reports shows %+v, want zero", got)
	}

	heartbeat(entryID, []map[string]any{
		{"entry_port": 24981, "transport": "tcp", "up_bytes": 1000, "down_bytes": 9000},
		{"entry_port": 24981, "transport": "udp", "up_bytes": 50, "down_bytes": 70},
		{"entry_port": 24982, "transport": "tcp", "up_bytes": 5, "down_bytes": 6},
		// The other Entry's port: this Entry does not carry it, so it is not attributed.
		{"entry_port": 24983, "transport": "tcp", "up_bytes": 777, "down_bytes": 777},
		// A UDP report for a TCP-only route matches nothing.
		{"entry_port": 24982, "transport": "udp", "up_bytes": 888, "down_bytes": 888},
		// Malformed entries are skipped, never fail the heartbeat.
		{"entry_port": 0, "transport": "tcp", "up_bytes": 1, "down_bytes": 1},
		{"entry_port": 24981, "transport": "tcp_udp", "up_bytes": 1, "down_bytes": 1},
		{"entry_port": 24981, "transport": "tcp", "up_bytes": -5, "down_bytes": 1},
	})
	heartbeat(entryID, []map[string]any{{"entry_port": 24981, "transport": "tcp", "up_bytes": 1, "down_bytes": 1}})
	heartbeat(poolMemberID, []map[string]any{{"entry_port": 24984, "transport": "udp", "up_bytes": 300, "down_bytes": 400}})

	chains := listed()
	for _, check := range []struct {
		name string
		id   string
		want services.RouteTrafficSummary
	}{
		{"tcp_udp route sums both protocols and both heartbeats", both, services.RouteTrafficSummary{UpBytes: 1051, DownBytes: 9071}},
		{"tcp route ignores a UDP report on its port", tcpOnly, services.RouteTrafficSummary{UpBytes: 5, DownBytes: 6}},
		{"another Entry's route is not credited", other, services.RouteTrafficSummary{}},
		{"legacy pool route is credited through its member", legacy, services.RouteTrafficSummary{UpBytes: 300, DownBytes: 400}},
	} {
		if got := traffic(chains, check.id); got != check.want {
			t.Errorf("%s: got %+v, want %+v", check.name, got, check.want)
		}
	}

	// Hours outside the window drop off the list.
	if _, err := pool.Exec(ctx, `UPDATE route_traffic_hourly SET bucket = bucket - interval '25 hours' WHERE chain_id = $1`, tcpOnly); err != nil {
		t.Fatal(err)
	}
	if got := traffic(listed(), tcpOnly); got != (services.RouteTrafficSummary{}) {
		t.Errorf("traffic older than the window is still shown: %+v", got)
	}

	// Direct routes have no Entry counting them, so they carry no figure.
	var directID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM node_chains WHERE exit_node_id = $1 AND entry_node_id IS NULL AND relay_pool_id IS NULL`, exitID).Scan(&directID); err == nil {
		if listed()[directID].Traffic != nil {
			t.Error("a direct route shows forwarded traffic")
		}
	}
}
