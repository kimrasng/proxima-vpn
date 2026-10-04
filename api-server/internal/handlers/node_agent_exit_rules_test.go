package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/server"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type exitRulesHTTPFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	app  *fiber.App
}

func newExitRulesHTTPFixture(t *testing.T) exitRulesHTTPFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed exit rules tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	t.Setenv("SWAGGER_ENABLED", "false")
	application := server.NewServer(&config.Config{}, pool, nil, nil, nil).App()
	t.Cleanup(func() { _ = application.Shutdown() })
	return exitRulesHTTPFixture{t: t, pool: pool, app: application}
}

func (f exitRulesHTTPFixture) seedNode(name, role, address string) string {
	f.t.Helper()
	var nodeID string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO nodes (name, api_key, ip, port, role, status)
		 VALUES ($1, 'test', $2::inet, 443, $3, 'offline') RETURNING id::text`,
		name, address, role,
	).Scan(&nodeID); err != nil {
		f.t.Fatalf("seed node %s: %v", name, err)
	}
	f.t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM nodes WHERE id = $1`, nodeID); err != nil {
			f.t.Errorf("clean up node %s: %v", name, err)
		}
	})
	return nodeID
}

func (f exitRulesHTTPFixture) seedGroup(name string) string {
	f.t.Helper()
	var groupID string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, name,
	).Scan(&groupID); err != nil {
		f.t.Fatalf("seed group %s: %v", name, err)
	}
	f.t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM node_chains WHERE relay_pool_id = $1`, groupID); err != nil {
			f.t.Errorf("clean up chains for group %s: %v", name, err)
		}
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM node_groups WHERE id = $1`, groupID); err != nil {
			f.t.Errorf("clean up group %s: %v", name, err)
		}
	})
	return groupID
}

func (f exitRulesHTTPFixture) addPoolMember(groupID, nodeID string) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, groupID, nodeID,
	); err != nil {
		f.t.Fatalf("add node %s to relay pool: %v", nodeID, err)
	}
}

func TestGetExitRulesProjectsEnabledRelayedChainsThroughAuthenticatedHTTP(t *testing.T) {
	fixture := newExitRulesHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := fixture.seedNode("exit-rules-exit-"+suffix, "exit", "203.0.113.50")
	relayA := fixture.seedNode("exit-rules-relay-a-"+suffix, "relay", "203.0.113.2")
	relayB := fixture.seedNode("exit-rules-relay-b-"+suffix, "both", "203.0.113.10")
	duplicateRelay := fixture.seedNode("exit-rules-relay-duplicate-"+suffix, "relay", "203.0.113.10")
	ineligibleNode := fixture.seedNode("exit-rules-ineligible-"+suffix, "exit", "203.0.113.5")
	ipv6Relay := fixture.seedNode("exit-rules-ipv6-"+suffix, "relay", "2001:db8::10")
	otherExitID := fixture.seedNode("exit-rules-other-exit-"+suffix, "exit", "203.0.113.60")
	poolID := fixture.seedGroup("exit-rules-pool-" + suffix)
	emptyPoolID := fixture.seedGroup("exit-rules-empty-pool-" + suffix)
	for _, nodeID := range []string{relayA, relayB, duplicateRelay, ineligibleNode, ipv6Relay} {
		fixture.addPoolMember(poolID, nodeID)
	}

	chains := []struct {
		name      string
		poolID    string
		exitID    string
		exitPort  int
		entryPort int
		transport string
		mode      string
		enabled   bool
	}{
		{"tcp", poolID, exitID, 443, 30001, "tcp", "l4_dnat", true},
		{"udp", poolID, exitID, 443, 30002, "udp", "l4_dnat", true},
		{"empty", emptyPoolID, exitID, 8443, 30003, "tcp", "l4_dnat", true},
		{"disabled", poolID, exitID, 9443, 30004, "tcp", "l4_dnat", false},
		{"wrong-mode", poolID, exitID, 10443, 30005, "tcp", "wg_tunnel", true},
		{"other-exit", poolID, otherExitID, 11443, 30006, "tcp", "l4_dnat", true},
	}
	for _, chain := range chains {
		if _, err := fixture.pool.Exec(ctx,
			`INSERT INTO node_chains
			   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport, mode, enabled)
			 VALUES ($1, $2, 'relay.example.test', $3, $4, $5, $6, $7, $8)`,
			"exit-rules-"+chain.name+"-"+suffix, chain.poolID, chain.entryPort, chain.exitID,
			chain.exitPort, chain.transport, chain.mode, chain.enabled,
		); err != nil {
			t.Fatalf("seed %s chain: %v", chain.name, err)
		}
	}
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 443, 'tcp_udp')`, "exit-rules-direct-"+suffix, exitID,
	); err != nil {
		t.Fatalf("seed direct chain: %v", err)
	}

	request := httptest.NewRequest(fiber.MethodGet, "/api/v1/nodes/"+exitID+"/exit-rules", nil)
	request.Header.Set("X-Node-Key", "test")
	response, err := fixture.app.Test(request)
	if err != nil {
		t.Fatalf("get exit rules: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read exit rules response: %v", err)
	}
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("exit rules status = %d, want 200; body=%s", response.StatusCode, body)
	}
	t.Logf("GET /api/v1/nodes/:id/exit-rules -> %d %s", response.StatusCode, body)
	want := []nodeprov.ExitRule{
		{ExitPort: 443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.2", "203.0.113.10"}},
		{ExitPort: 443, Transport: nodeprov.TransportUDP, RelayIPs: []string{"203.0.113.2", "203.0.113.10"}},
		{ExitPort: 8443, Transport: nodeprov.TransportTCP, RelayIPs: []string{}},
	}
	if encoded, expected := string(body), mustMarshalExitRules(t, want); encoded != expected {
		t.Errorf("exit rules response = %s, want %s", encoded, expected)
	}
}

func TestGetExitRulesRequiresNodeAuthenticationThroughHTTP(t *testing.T) {
	fixture := newExitRulesHTTPFixture(t)
	exitID := fixture.seedNode("exit-rules-auth-"+crypto.NewUUID(), "exit", "203.0.113.70")

	response, err := fixture.app.Test(httptest.NewRequest(
		fiber.MethodGet, "/api/v1/nodes/"+exitID+"/exit-rules", nil,
	))
	if err != nil {
		t.Fatalf("get exit rules without credentials: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("unauthenticated exit rules status = %d, want 401", response.StatusCode)
	}
}

func TestGetExitRulesProjectsExplicitEntryNodeIPThroughAuthenticatedHTTP(t *testing.T) {
	fixture := newExitRulesHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := fixture.seedNode("exit-rules-explicit-exit-"+suffix, "exit", "203.0.113.80")
	entryID := fixture.seedNode("exit-rules-explicit-entry-"+suffix, "relay", "203.0.113.81")
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO node_chains
		   (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, '203.0.113.81', 30101, $3, 443, 'tcp')`,
		"exit-rules-explicit-"+suffix, entryID, exitID,
	); err != nil {
		t.Fatalf("seed explicit chain: %v", err)
	}
	t.Cleanup(func() {
		if _, err := fixture.pool.Exec(ctx, `DELETE FROM node_chains WHERE entry_node_id = $1`, entryID); err != nil {
			t.Errorf("clean up explicit chain: %v", err)
		}
	})

	request := httptest.NewRequest(fiber.MethodGet, "/api/v1/nodes/"+exitID+"/exit-rules", nil)
	request.Header.Set("X-Node-Key", "test")
	response, err := fixture.app.Test(request)
	if err != nil {
		t.Fatalf("get explicit exit rules: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read explicit exit rules: %v", err)
	}
	want := []nodeprov.ExitRule{{ExitPort: 443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.81"}}}
	if encoded, expected := string(body), mustMarshalExitRules(t, want); encoded != expected {
		t.Errorf("explicit exit rules response = %s, want %s", encoded, expected)
	}
}

func TestGetRelayRulesSelectsOnlyTheExplicitEntryNode(t *testing.T) {
	fixture := newExitRulesHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := fixture.seedNode("explicit-exit-"+suffix, "exit", "203.0.113.90")
	entryID := fixture.seedNode("explicit-entry-"+suffix, "relay", "203.0.113.91")
	otherID := fixture.seedNode("unrelated-entry-"+suffix, "relay", "203.0.113.92")
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO node_chains (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 'relay.example.test', 30102, $3, 443, 'tcp')`,
		"explicit-relay-"+suffix, entryID, exitID,
	); err != nil {
		t.Fatalf("seed explicit chain: %v", err)
	}
	t.Cleanup(func() {
		if _, err := fixture.pool.Exec(ctx, `DELETE FROM node_chains WHERE entry_node_id = $1`, entryID); err != nil {
			t.Errorf("clean up explicit chain: %v", err)
		}
	})
	for _, testCase := range []struct {
		name   string
		id     string
		length int
	}{
		{name: "selected entry", id: entryID, length: 1},
		{name: "other entry", id: otherID, length: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(fiber.MethodGet, "/api/v1/nodes/"+testCase.id+"/relay-rules", nil)
			request.Header.Set("X-Node-Key", "test")
			response, err := fixture.app.Test(request)
			if err != nil {
				t.Fatalf("get relay rules: %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != fiber.StatusOK {
				t.Fatalf("relay rules status = %d", response.StatusCode)
			}
			var rules []nodeprov.RelayRule
			if err := json.NewDecoder(response.Body).Decode(&rules); err != nil {
				t.Fatalf("decode relay rules: %v", err)
			}
			if len(rules) != testCase.length {
				t.Fatalf("relay rules = %+v, want %d", rules, testCase.length)
			}
			if len(rules) == 1 && (rules[0].ExitIP != "203.0.113.90" || rules[0].EntryPort != 30102) {
				t.Errorf("relay destination = %+v", rules[0])
			}
		})
	}
}

func mustMarshalExitRules(t *testing.T, rules []nodeprov.ExitRule) string {
	t.Helper()
	body, err := json.Marshal(rules)
	if err != nil {
		t.Fatalf("marshal expected exit rules: %v", err)
	}
	return string(body)
}
