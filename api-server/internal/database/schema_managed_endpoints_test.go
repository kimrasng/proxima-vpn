package database

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func phs028DB(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := testDB(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate real Postgres: %v", err)
	}
	return pool, ctx
}

func phs028Node(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	ctx := t.Context()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO nodes (name, api_key, ip) VALUES ($1, 'test', '203.0.113.9') RETURNING id::text`, name).Scan(&id); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx), `DELETE FROM nodes WHERE id = $1`, id); err != nil {
			t.Errorf("clean node: %v", err)
		}
	})
	return id
}

func phs028SQLState(t *testing.T, err error, state string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != state {
		t.Fatalf("got error %v, want SQLSTATE %s", err, state)
	}
}

func TestPHS028FreshSchemaStoresNullableSNIAndManagedDNS(t *testing.T) {
	// Given a freshly migrated real database.
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	// When a valid explicit SNI and DNS intent are persisted.
	var sni, source, status *string
	if err := pool.QueryRow(ctx, `SELECT reality_client_sni, reality_sni_source, reality_sni_status FROM nodes WHERE id = $1`, node).Scan(&sni, &source, &status); err != nil {
		t.Fatalf("read nullable SNI fields: %v", err)
	}
	if sni != nil || source != nil || status != nil {
		t.Fatalf("new node should be unevaluated: %v %v %v", sni, source, status)
	}
	var id, owner, live, hostname, desired string
	err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname, desired_ipv4, desired_action, dns_status) VALUES ($1, $1, 'entry.example.test', '203.0.113.9', 'present', 'pending') RETURNING id::text, owner_node_id::text, node_id::text, hostname, host(desired_ipv4)`, node).Scan(&id, &owner, &live, &hostname, &desired)
	if err != nil {
		t.Fatalf("persist managed DNS: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id = $1`, id) })
	// Then the UUID and complete intent round-trip without implicitly evaluating SNI.
	if id == "" || owner != node || live != node || hostname != "entry.example.test" || desired != "203.0.113.9" {
		t.Fatalf("unexpected intent: %q %q %q %q %q", id, owner, live, hostname, desired)
	}
}

func TestPHS028ManagedDNSDefaultsToPresentWhenActionOmitted(t *testing.T) {
	pool, ctx := phs028DB(t)
	var id, action string
	err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns (owner_node_id) VALUES (gen_random_uuid()) RETURNING id::text, desired_action`).Scan(&id, &action)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id::text = $1`, id) })
	if err != nil || action != "present" {
		t.Fatalf("insert minimal DNS intent: action=%q, err=%v; want present", action, err)
	}
}

func TestPHS028MigrationTwicePreservesDNSIntentAndSNI(t *testing.T) {
	// Given persisted intent and a visible conflict with no canonical SNI.
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	phs028Inbound(t, pool, node, "vless_reality", `{"server_names":["bad name"]}`, true)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_sni_source = 'backfill', reality_sni_status = 'conflict', reality_sni_error_code = 'invalid_sni' WHERE id = $1`, node); err != nil {
		t.Fatalf("seed conflict: %v", err)
	}
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname, desired_action, dns_status, cleanup_requested_at) VALUES ($1, $1, 'stable.example.test', 'delete', 'deleting', NOW()) RETURNING id::text`, node).Scan(&id); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id = $1`, id) })
	// When the entire migration runs twice more.
	for range 2 {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("repeat migration: %v", err)
		}
	}
	// Then the same intent and null canonical SNI survive.
	var gotID, host, status string
	var sni *string
	if err := pool.QueryRow(ctx, `SELECT id::text, hostname, dns_status FROM managed_entry_dns WHERE owner_node_id = $1`, node).Scan(&gotID, &host, &status); err != nil {
		t.Fatalf("read retained intent: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT reality_client_sni FROM nodes WHERE id = $1`, node).Scan(&sni); err != nil {
		t.Fatalf("read retained SNI: %v", err)
	}
	if gotID != id || host != "stable.example.test" || status != "deleting" || sni != nil {
		t.Fatalf("migration rewrote intent or canonical SNI: %q %q %q %v", gotID, host, status, sni)
	}
}

func TestPHS028ConcurrentMigrationsPreserveManagedContract(t *testing.T) {
	// Given a migrated database and two independent callers.
	pool, ctx := phs028DB(t)
	result := make(chan error, 2)
	// When both migrate at the same time under the production advisory lock.
	for range 2 {
		go func() { result <- Migrate(ctx, pool) }()
	}
	// Then both succeed and the contract still exists.
	for range 2 {
		err := <-result
		if err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('managed_entry_dns') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("managed contract missing: exists=%v err=%v", exists, err)
	}
}

func TestPHS028ManagedDNSRejectsDuplicateLiveNodeAndHostname(t *testing.T) {
	pool, ctx := phs028DB(t)
	first := phs028Node(t, pool, t.Name()+"-first")
	_, err := pool.Exec(ctx, `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname, desired_action) VALUES ($1, $1, 'unique.example.test', 'present')`, first)
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE owner_node_id = $1`, first) })
	for name, query := range map[string]string{
		"owner":     `INSERT INTO managed_entry_dns (owner_node_id, desired_action) VALUES ($1, 'present')`,
		"live node": `INSERT INTO managed_entry_dns (owner_node_id, node_id, desired_action) VALUES (gen_random_uuid(), $1, 'present')`,
		"hostname":  `INSERT INTO managed_entry_dns (owner_node_id, hostname, desired_action) VALUES (gen_random_uuid(), $1, 'present')`,
	} {
		t.Run(name, func(t *testing.T) {
			arg := first
			if name == "hostname" {
				arg = "unique.example.test"
			}
			_, err := pool.Exec(ctx, query, arg)
			phs028SQLState(t, err, "23505")
		})
	}
}

func TestPHS028ManagedDNSOwnerIdentityIsImmutable(t *testing.T) {
	pool, ctx := phs028DB(t)
	owner := phs028Node(t, pool, t.Name()+"-owner")
	replacement := phs028Node(t, pool, t.Name()+"-replacement")
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns (owner_node_id, desired_action) VALUES ($1, 'present') RETURNING id::text`, owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id = $1`, id) })
	_, err := pool.Exec(ctx, `UPDATE managed_entry_dns SET owner_node_id = $1 WHERE id = $2`, replacement, id)
	phs028SQLState(t, err, "23514")
}

func TestPHS028ManagedDNSRejectsInvalidEnumsAndIPv4(t *testing.T) {
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	for name, query := range map[string]string{
		"action":               `INSERT INTO managed_entry_dns (owner_node_id, desired_action) VALUES ($1, 'purge')`,
		"dns status":           `INSERT INTO managed_entry_dns (owner_node_id, desired_action, dns_status) VALUES ($1, 'present', 'unknown')`,
		"error code":           `INSERT INTO managed_entry_dns (owner_node_id, desired_action, error_code) VALUES ($1, 'present', 'arbitrary_provider_text')`,
		"ipv6 desired":         `INSERT INTO managed_entry_dns (owner_node_id, desired_action, desired_ipv4) VALUES ($1, 'present', '2001:db8::1')`,
		"unspecified desired":  `INSERT INTO managed_entry_dns (owner_node_id, desired_action, desired_ipv4) VALUES ($1, 'present', '0.0.0.0')`,
		"ipv6 observed":        `INSERT INTO managed_entry_dns (owner_node_id, desired_action, observed_ipv4) VALUES ($1, 'present', '2001:db8::1')`,
		"unspecified observed": `INSERT INTO managed_entry_dns (owner_node_id, desired_action, observed_ipv4) VALUES ($1, 'present', '0.0.0.0')`,
	} {
		t.Run(name, func(t *testing.T) { _, err := pool.Exec(ctx, query, node); phs028SQLState(t, err, "23514") })
	}
	for name, query := range map[string]string{
		"source":              `UPDATE nodes SET reality_sni_source = 'provider' WHERE id = $1`,
		"status":              `UPDATE nodes SET reality_sni_status = 'unknown' WHERE id = $1`,
		"error":               `UPDATE nodes SET reality_sni_error_code = 'arbitrary_provider_text' WHERE id = $1`,
		"valid without name":  `UPDATE nodes SET reality_sni_source = 'admin', reality_sni_status = 'valid' WHERE id = $1`,
		"name without source": `UPDATE nodes SET reality_client_sni = 'exit.example.test' WHERE id = $1`,
	} {
		t.Run(name, func(t *testing.T) { _, err := pool.Exec(ctx, query, node); phs028SQLState(t, err, "23514") })
	}
}

func TestPHS028NodeDeletionRetainsDNSOwnershipAndCleanup(t *testing.T) {
	pool, ctx := phs028DB(t)
	var node string
	if err := pool.QueryRow(ctx, `INSERT INTO nodes (name, api_key, ip) VALUES ($1, 'test', '203.0.113.5') RETURNING id::text`, t.Name()).Scan(&node); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, node) }()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname, desired_action, dns_status, observed_ipv4, observed_at, cleanup_requested_at) VALUES ($1, $1, 'retain.example.test', 'delete', 'deleting', '203.0.113.5', NOW(), NOW()) RETURNING id::text`, node).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id = $1`, id) })
	// When the live node is removed.
	if _, err := pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, node); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	// Then the durable owner, hostname, observation and cleanup request remain, with only live node cleared.
	var owner, hostname, observed, action, status string
	var live *string
	var hasObservation, hasCleanup bool
	err := pool.QueryRow(ctx, `SELECT owner_node_id::text, node_id::text, hostname, host(observed_ipv4), desired_action, dns_status, observed_at IS NOT NULL, cleanup_requested_at IS NOT NULL FROM managed_entry_dns WHERE id = $1`, id).Scan(&owner, &live, &hostname, &observed, &action, &status, &hasObservation, &hasCleanup)
	if err != nil || owner != node || live != nil || hostname != "retain.example.test" || observed != "203.0.113.5" || action != "delete" || status != "deleting" || !hasObservation || !hasCleanup {
		t.Fatalf("lost durable intent: owner=%q live=%v host=%q ip=%q action=%q status=%q observed=%v cleanup=%v err=%v", owner, live, hostname, observed, action, status, hasObservation, hasCleanup, err)
	}
}

func TestPHS028MigrationPreservesDirectExplicitAndPoolChains(t *testing.T) {
	pool, ctx := phs028DB(t)
	exit := phs028Node(t, pool, t.Name()+"-exit")
	entry := phs028Node(t, pool, t.Name()+"-entry")
	var group string
	if err := pool.QueryRow(ctx, `INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, t.Name()).Scan(&group); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, group) })
	if _, err := pool.Exec(ctx, `INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, group, entry); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, args := range []struct {
		name        string
		entry, pool *string
		host        string
		port        *int
	}{
		{name: "direct", host: ""},
		{name: "explicit", entry: &entry, host: "legacy-entry.example.test", port: phs028Port(27101)},
		{name: "pool", pool: &group, host: "legacy-pool.example.test", port: phs028Port(27102)},
	} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO node_chains (name, entry_node_id, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport, mode, priority, enabled) VALUES ($1, $2, $3, $4, $5, $6, 8443, 'udp', 'wg_tunnel', 7, false) RETURNING id::text`, t.Name()+args.name, args.entry, args.pool, args.host, args.port, exit).Scan(&id); err != nil {
			t.Fatalf("seed %s chain: %v", args.name, err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM node_chains WHERE id::text = ANY($1)`, ids) })
	if _, err := pool.Exec(ctx, `INSERT INTO node_group_chains (node_group_id, chain_id) SELECT $1, id FROM node_chains WHERE id::text = ANY($2)`, group, ids); err != nil {
		t.Fatal(err)
	}
	// Given the byte-level JSONB representations of all representative chains and their group assignments.
	before := phs028ChainSnapshot(t, pool, ids)
	// When the production migration runs again.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate legacy chains: %v", err)
	}
	// Then every stored value, identity, assignment and endpoint remains identical.
	after := phs028ChainSnapshot(t, pool, ids)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("migration rewrote chain/group rows:\nbefore %s\nafter %s", before, after)
	}
}

func phs028Port(port int) *int { return &port }

func phs028ChainSnapshot(t *testing.T, pool *pgxpool.Pool, ids []string) json.RawMessage {
	t.Helper()
	var snapshot json.RawMessage
	err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object('chains', (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM node_chains c WHERE c.id::text = ANY($1)), 'groups', (SELECT jsonb_agg(to_jsonb(g) ORDER BY g.node_group_id, g.chain_id) FROM node_group_chains g WHERE g.chain_id::text = ANY($1)), 'ports', (SELECT jsonb_agg(indexdef ORDER BY indexname) FROM pg_indexes WHERE indexname IN ('idx_node_chains_entry_port_tcp_claim', 'idx_node_chains_entry_port_udp_claim')))`, ids).Scan(&snapshot)
	if err != nil {
		t.Fatalf("snapshot chains: %v", err)
	}
	return snapshot
}
