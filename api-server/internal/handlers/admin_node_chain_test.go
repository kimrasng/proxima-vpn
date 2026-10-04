package handlers

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func chainTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed chain tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedChainNode(t *testing.T, pool *pgxpool.Pool, name, role string, port int) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, api_key, ip, port, role, status)
		 VALUES ($1, 'test', '203.0.113.50'::inet, $2, $3, 'offline') RETURNING id::text`,
		name, port, role,
	).Scan(&id); err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_chains WHERE entry_node_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, id)
	})
	return id
}

func seedChainGroup(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, name,
	).Scan(&id); err != nil {
		t.Fatalf("seed group %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_chains WHERE relay_pool_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, id)
	})
	return id
}

// Subscriptions are served from node_group_chains, so the entry port a chain
// claims has to be free across the whole fleet - not just within one pool. A
// second pool reusing a number would produce two rules that cannot both be
// installed on a relay that later joins both.
func TestEntryPortIsUniqueAcrossPools(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()

	exitID := seedChainNode(t, pool, "chain-api-exit", "exit", 443)
	poolA := seedChainGroup(t, pool, "chain-api-pool-a")
	poolB := seedChainGroup(t, pool, "chain-api-pool-b")

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('a', $1, 'relay.example.test', $2, 443, 23501, 'tcp')`,
		poolA, exitID,
	); err != nil {
		t.Fatalf("first chain rejected: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('b', $1, 'relay.example.test', $2, 2083, 23501, 'tcp')`,
		poolB, exitID,
	); err == nil {
		t.Error("a second pool claimed entry port 23501/tcp; entry ports must be fleet-wide unique")
	}
}

// A node with no chain is invisible to every client, whatever groups it is in.
// Registration is where the node reports the port its chain must carry, so the
// chain has to be created there rather than when the token was issued.
func TestRegistrationCreatesTheDirectChain(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()

	// Mirrors what Register does after validating the token.
	nodeID := seedChainNode(t, pool, "chain-api-fresh", "exit", 8443)
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, exit_node_id, exit_port, transport)
		 SELECT n.name, NULL, n.id, n.port, 'tcp_udp'
		 FROM nodes n
		 WHERE n.id = $1
		   AND NOT EXISTS (
		     SELECT 1 FROM node_chains c
		     WHERE c.exit_node_id = n.id AND c.relay_pool_id IS NULL
		   )`,
		nodeID,
	); err != nil {
		t.Fatalf("create direct chain: %v", err)
	}

	var count, exitPort int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(MAX(exit_port), 0) FROM node_chains
		 WHERE exit_node_id = $1 AND relay_pool_id IS NULL`,
		nodeID,
	).Scan(&count, &exitPort); err != nil {
		t.Fatalf("count chains: %v", err)
	}
	if count != 1 {
		t.Errorf("node has %d direct chains, want exactly 1", count)
	}
	if exitPort != 8443 {
		t.Errorf("chain exit_port = %d, want the port the node registered on (8443)", exitPort)
	}

	// Re-registering must not mint a second chain.
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, exit_node_id, exit_port, transport)
		 SELECT n.name, NULL, n.id, n.port, 'tcp_udp'
		 FROM nodes n
		 WHERE n.id = $1
		   AND NOT EXISTS (
		     SELECT 1 FROM node_chains c
		     WHERE c.exit_node_id = n.id AND c.relay_pool_id IS NULL
		   )`,
		nodeID,
	); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_chains WHERE exit_node_id = $1 AND relay_pool_id IS NULL`,
		nodeID,
	).Scan(&count); err != nil {
		t.Fatalf("recount chains: %v", err)
	}
	if count != 1 {
		t.Errorf("re-registering produced %d chains, want 1", count)
	}
}

// A membership edit that touched only node_group_nodes would not reach a single
// client, because the subscription query reads node_group_chains. The two have to
// move together.
func TestGroupMembershipEditReachesSubscriptions(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()

	exitID := seedChainNode(t, pool, "chain-api-member", "exit", 443)
	groupID := seedChainGroup(t, pool, "chain-api-group")

	// What SetNodes runs: membership, then the chain, then the attachment.
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`,
		groupID, exitID,
	); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, exit_node_id, exit_port, transport)
		 SELECT n.name, NULL, n.id, n.port, 'tcp_udp'
		 FROM nodes n
		 WHERE n.id = ANY($1::uuid[])
		   AND NOT EXISTS (
		     SELECT 1 FROM node_chains c
		     WHERE c.exit_node_id = n.id AND c.relay_pool_id IS NULL
		   )`,
		[]string{exitID},
	); err != nil {
		t.Fatalf("create chain: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id)
		 SELECT $1, c.id FROM node_chains c
		 WHERE c.relay_pool_id IS NULL AND c.exit_node_id = ANY($2::uuid[])
		 ON CONFLICT DO NOTHING`,
		groupID, []string{exitID},
	); err != nil {
		t.Fatalf("attach chain: %v", err)
	}

	var attached int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_group_chains ngc
		 JOIN node_chains c ON c.id = ngc.chain_id
		 WHERE ngc.node_group_id = $1 AND c.exit_node_id = $2`,
		groupID, exitID,
	).Scan(&attached); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if attached != 1 {
		t.Fatalf("group has %d chains for the added node, want 1: the edit never reached subscriptions", attached)
	}

	// Removing the node from the group must withdraw its chain too, or a removed
	// node keeps being handed out.
	if _, err := pool.Exec(ctx,
		`DELETE FROM node_group_chains ngc
		 USING node_chains c
		 WHERE ngc.chain_id = c.id
		   AND ngc.node_group_id = $1
		   AND c.relay_pool_id IS NULL
		   AND NOT (c.exit_node_id = ANY($2::uuid[]))`,
		groupID, []string{},
	); err != nil {
		t.Fatalf("withdraw chain: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_group_chains WHERE node_group_id = $1`, groupID,
	).Scan(&attached); err != nil {
		t.Fatalf("recount attachments: %v", err)
	}
	if attached != 0 {
		t.Errorf("group still serves %d chains after the node was removed", attached)
	}
}

// Deleting a chain frees its entry port; without that the range leaks a port on
// every rebuild and eventually cannot allocate.
func TestDeletingAChainFreesItsEntryPort(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()

	exitID := seedChainNode(t, pool, "chain-api-reuse", "exit", 443)
	poolID := seedChainGroup(t, pool, "chain-api-reuse-pool")

	var chainID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('x', $1, 'relay.example.test', $2, 443, 23502, 'tcp') RETURNING id::text`,
		poolID, exitID,
	).Scan(&chainID); err != nil {
		t.Fatalf("create chain: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM node_chains WHERE id = $1`, chainID); err != nil {
		t.Fatalf("delete chain: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ('y', $1, 'relay.example.test', $2, 443, 23502, 'tcp')`,
		poolID, exitID,
	); err != nil {
		t.Errorf("entry port 23502 was not reusable after its chain was deleted: %v", err)
	}
}
