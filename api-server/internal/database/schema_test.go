package database

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed schema tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Test_PlanOrdersCheckoutAudit verifies that plan_orders carries the checkout
// audit and grant-evidence columns with correct types and nullability.
// NULL tracking fields indicate never-captured data, not purged data; the
// retention sweep NULLs tracking fields while preserving grant timestamps.
// Tracking: origin, client_ip, user_agent, browser_family, os_family, locale, device_fingerprint
// Grant evidence: granted_plan_expires_before, granted_plan_expires_after
func Test_PlanOrdersCheckoutAudit(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	// Run migrations to ensure schema is current.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Check that all tracking columns exist and are nullable TEXT.
	trackingCols := []string{
		"origin", "client_ip", "user_agent", "browser_family",
		"os_family", "locale", "device_fingerprint",
	}
	for _, col := range trackingCols {
		var colType, isNullable string
		if err := pool.QueryRow(ctx, `
			SELECT data_type, is_nullable
			FROM information_schema.columns
			WHERE table_name = 'plan_orders' AND column_name = $1
		`, col).Scan(&colType, &isNullable); err != nil {
			t.Errorf("tracking column %q missing: %v", col, err)
			continue
		}
		if colType != "text" {
			t.Errorf("tracking column %q type = %q, want text", col, colType)
		}
		if isNullable != "YES" {
			t.Errorf("tracking column %q is_nullable = %q, want YES", col, isNullable)
		}
	}

	// Check that grant-evidence columns exist and are nullable TIMESTAMPTZ.
	grantCols := map[string]string{
		"granted_plan_expires_before": "timestamp with time zone",
		"granted_plan_expires_after":  "timestamp with time zone",
	}
	for col, expectedType := range grantCols {
		var colType, isNullable string
		if err := pool.QueryRow(ctx, `
			SELECT data_type, is_nullable
			FROM information_schema.columns
			WHERE table_name = 'plan_orders' AND column_name = $1
		`, col).Scan(&colType, &isNullable); err != nil {
			t.Errorf("grant-evidence column %q missing: %v", col, err)
			continue
		}
		if colType != expectedType {
			t.Errorf("grant-evidence column %q type = %q, want %q", col, colType, expectedType)
		}
		if isNullable != "YES" {
			t.Errorf("grant-evidence column %q is_nullable = %q, want YES", col, isNullable)
		}
	}

	// Check that the partial purge index exists to support retention sweeps.
	// The index is on created_at where user_agent is still present (non-NULL user_agent
	// is the sweep termination predicate for tracking nullification).
	var indexExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_indexes
			WHERE tablename = 'plan_orders' AND indexname = 'idx_plan_orders_purge_sweep'
		)
	`).Scan(&indexExists); err != nil {
		t.Fatalf("check purge index existence: %v", err)
	}
	if !indexExists {
		t.Error("partial purge index idx_plan_orders_purge_sweep does not exist")
	}

	// Verify the index predicate is exactly "(user_agent IS NOT NULL)" —
	// this is the retention sweep termination condition.
	var indexDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'plan_orders' AND indexname = 'idx_plan_orders_purge_sweep'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("fetch purge index definition: %v", err)
	}
	if !contains(indexDef, "user_agent IS NOT NULL") {
		t.Errorf("purge index predicate missing 'user_agent IS NOT NULL': %s", indexDef)
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test_NodeChainsBackfill asserts the invariants a relay chain must hold: every
// pre-existing node keeps a direct chain (otherwise subscriptions, which read
// chains rather than node_group_nodes, would go empty on the first boot after
// the migration), group membership carries over to those chains, the migration
// is idempotent, and the entry_port/relay_pool_id pairing plus fleet-wide entry
// port uniqueness are enforced by the database rather than by callers.
func Test_NodeChainsBackfill(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var groupID, exitID, relayID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ('chain-test-group') RETURNING id::text`,
	).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, groupID)
	})

	for _, seed := range []struct {
		name string
		role string
		dest *string
	}{{name: "chain-test-exit", role: "exit"}, {name: "chain-test-relay", role: "relay"}} {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO nodes (name, api_key, ip, port, role)
			 VALUES ($1, 'test', '203.0.113.1'::inet, 443, $2) RETURNING id::text`,
			seed.name, seed.role,
		).Scan(&id); err != nil {
			t.Fatalf("seed node %s: %v", seed.name, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, id)
		})
		if seed.role == "exit" {
			exitID = id
		} else {
			relayID = id
		}
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`,
		groupID, exitID,
	); err != nil {
		t.Fatalf("seed group membership: %v", err)
	}

	// The nodes were inserted after the first Migrate, so this run is what
	// backfills them - and also proves a second run is safe.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate (second run): %v", err)
	}

	var directChains int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_chains WHERE exit_node_id = $1 AND relay_pool_id IS NULL`,
		exitID,
	).Scan(&directChains); err != nil {
		t.Fatalf("count direct chains: %v", err)
	}
	if directChains != 1 {
		t.Errorf("exit node has %d direct chains, want exactly 1", directChains)
	}

	var linkedChains int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_group_chains ngc
		 JOIN node_chains c ON c.id = ngc.chain_id
		 WHERE ngc.node_group_id = $1 AND c.exit_node_id = $2`,
		groupID, exitID,
	).Scan(&linkedChains); err != nil {
		t.Fatalf("count group chains: %v", err)
	}
	if linkedChains != 1 {
		t.Errorf("group has %d chains for the seeded exit, want 1", linkedChains)
	}

	var poolID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ('chain-test-relay-pool') RETURNING id::text`,
	).Scan(&poolID); err != nil {
		t.Fatalf("seed relay pool: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_chains WHERE relay_pool_id = $1`, poolID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, poolID)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`,
		poolID, relayID,
	); err != nil {
		t.Fatalf("seed relay into pool: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('bad', $1, 'relay.example.test', $2, 443, NULL, 'tcp')`,
		poolID, exitID,
	); err == nil {
		t.Error("a relayed chain with a NULL entry_port was accepted; chain_entry_set is not enforced")
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('ok-tcp', $1, 'relay.example.test', $2, 443, 23001, 'tcp')`,
		poolID, exitID,
	); err != nil {
		t.Fatalf("valid relayed chain rejected: %v", err)
	}

	// Same port, different transport: legitimate, since one pool can front a TCP
	// Reality exit and a UDP Hysteria2 exit on the same port number.
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('ok-udp', $1, 'relay.example.test', $2, 8443, 23001, 'udp')`,
		poolID, exitID,
	); err != nil {
		t.Errorf("tcp and udp chains on one entry port were rejected: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('dup-tcp', $1, 'relay.example.test', $2, 2083, 23001, 'tcp')`,
		poolID, exitID,
	); err == nil {
		t.Error("two tcp chains claimed the same entry port; the uniqueness index is not enforced")
	}

	// A second pool must not be able to reuse the port either: rules are
	// replicated per pool and a relay may later join both.
	var otherPoolID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ('chain-test-relay-pool-2') RETURNING id::text`,
	).Scan(&otherPoolID); err != nil {
		t.Fatalf("seed second relay pool: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_chains WHERE relay_pool_id = $1`, otherPoolID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, otherPoolID)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
			 VALUES ('cross-pool', $1, 'relay.example.test', $2, 2083, 23001, 'tcp')`,
		otherPoolID, exitID,
	); err == nil {
		t.Error("a second pool reused entry port 23001/tcp; entry ports must be unique fleet-wide")
	}
}
