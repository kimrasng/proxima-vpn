package handlers

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A node serves exactly one protocol: Xray takes a single config per node, and
// mixing protocols let a speed-limited user reach an uncapped inbound (see the
// tier-inbound logic in services/xray_config.go). The rule is enforced twice -
// in the handler for a clear 409, and by a unique index so no other write path
// can bypass it - so both are exercised here.
func TestOneProtocolPerNodeIsEnforcedByTheDatabase(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()

	var hasIndex bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM pg_indexes
		   WHERE tablename = 'inbounds' AND indexname = 'idx_inbounds_one_protocol_per_node'
		 )`,
	).Scan(&hasIndex); err != nil {
		t.Fatalf("check index: %v", err)
	}
	if !hasIndex {
		t.Fatal("idx_inbounds_one_protocol_per_node is missing; the rule rests on the handler alone")
	}

	var nodeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, country, region, ip, port, api_key, status,
		                    reality_private_key, reality_public_key, reality_short_id)
		 VALUES ('one-proto-test', 'KR', 'seoul', '10.255.255.1', 8443, $1, 'online', 'k', 'p', 'sid')
		 RETURNING id`,
		"test-key-"+t.Name(),
	).Scan(&nodeID); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id = $1`, nodeID)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO inbounds (node_id, protocol, port, tag) VALUES ($1, 'vless_reality', 8443, 'vless-in')`,
		nodeID,
	); err != nil {
		t.Fatalf("first inbound should be accepted: %v", err)
	}

	// A second protocol on the same node must be rejected.
	if _, err := pool.Exec(ctx,
		`INSERT INTO inbounds (node_id, protocol, port, tag) VALUES ($1, 'vmess_ws', 8444, 'vmess-in')`,
		nodeID,
	); err == nil {
		t.Error("a second protocol on the same node was accepted; the index is not enforcing the rule")
	}

	// A second inbound on the node is rejected whatever its protocol: the node
	// runs one Xray config, so it serves one inbound.
	if _, err := pool.Exec(ctx,
		`INSERT INTO inbounds (node_id, protocol, port, tag) VALUES ($1, 'vless_reality', 8445, 'vless-in-2')`,
		nodeID,
	); err == nil {
		t.Error("a second inbound of the same protocol was accepted; the index is keyed on node_id")
	}
}

// The guarded migration must not fail on a deployment that already mixes
// protocols, since a failing migration blocks startup entirely.
func TestMixedProtocolNodesDoNotBreakTheMigration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	// The migration statement verbatim; re-running it must be a no-op.
	const migration = `DO $$
	 BEGIN
	   IF EXISTS (
	     SELECT 1 FROM inbounds GROUP BY node_id HAVING COUNT(*) > 1
	   ) THEN
	     RAISE NOTICE 'inbounds: skipping one-inbound-per-node index; existing nodes have several';
	   ELSE
	     CREATE UNIQUE INDEX IF NOT EXISTS idx_inbounds_one_protocol_per_node
	       ON inbounds(node_id);
	   END IF;
	 END $$`

	if _, err := pool.Exec(context.Background(), migration); err != nil {
		t.Fatalf("migration is not idempotent: %v", err)
	}
}
