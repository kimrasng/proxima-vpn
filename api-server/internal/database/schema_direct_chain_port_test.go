package database

import (
	"testing"
)

// directExitPort reads the exit_port of fixture's direct chain.
func directExitPort(t *testing.T, fixture relayPortFixture) int {
	t.Helper()
	var port int
	if err := fixture.pool.QueryRow(fixture.ctx,
		`SELECT exit_port FROM node_chains
		 WHERE exit_node_id = $1 AND relay_pool_id IS NULL AND entry_node_id IS NULL`,
		fixture.exitID,
	).Scan(&port); err != nil {
		t.Fatalf("read direct chain exit_port: %v", err)
	}
	return port
}

func addInbound(t *testing.T, fixture relayPortFixture, port int) string {
	t.Helper()
	var id string
	if err := fixture.pool.QueryRow(fixture.ctx,
		`INSERT INTO inbounds (node_id, protocol, port, tag, settings)
		 VALUES ($1, 'vless_reality', $2, 'vless-in', '{}') RETURNING id::text`,
		fixture.exitID, port,
	).Scan(&id); err != nil {
		t.Fatalf("insert inbound: %v", err)
	}
	return id
}

func exec(t *testing.T, fixture relayPortFixture, sql string, args ...any) {
	t.Helper()
	if _, err := fixture.pool.Exec(fixture.ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func TestDirectChainExitPort_followsTheExitsListeningPort(t *testing.T) {
	fixture := newRelayPortFixture(t) // node registered on 443, no inbound

	// A writer copying the node's registration port is corrected on insert.
	exec(t, fixture, `INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
		VALUES ('direct', $1, 9999, 'tcp_udp')`, fixture.exitID)
	if got := directExitPort(t, fixture); got != 443 {
		t.Fatalf("no inbound: exit_port = %d, want the node port 443", got)
	}

	// The operator's inbound on another port is what Xray listens on.
	inbound := addInbound(t, fixture, 20100)
	if got := directExitPort(t, fixture); got != 20100 {
		t.Fatalf("after inbound create: exit_port = %d, want 20100", got)
	}

	exec(t, fixture, `UPDATE inbounds SET port = 20200 WHERE id = $1`, inbound)
	if got := directExitPort(t, fixture); got != 20200 {
		t.Fatalf("after inbound port change: exit_port = %d, want 20200", got)
	}

	// Re-registration rewrites exit_port from the agent's port; it must not win.
	exec(t, fixture, `UPDATE node_chains SET exit_port = 443
		WHERE exit_node_id = $1 AND relay_pool_id IS NULL AND entry_node_id IS NULL`, fixture.exitID)
	if got := directExitPort(t, fixture); got != 20200 {
		t.Fatalf("after registration rewrite: exit_port = %d, want 20200", got)
	}

	// A disabled inbound is not served, so the node falls back to legacy nodes.port.
	exec(t, fixture, `UPDATE inbounds SET enabled = false WHERE id = $1`, inbound)
	if got := directExitPort(t, fixture); got != 443 {
		t.Fatalf("after inbound disable: exit_port = %d, want 443", got)
	}
	exec(t, fixture, `UPDATE inbounds SET enabled = true WHERE id = $1`, inbound)
	exec(t, fixture, `DELETE FROM inbounds WHERE id = $1`, inbound)
	if got := directExitPort(t, fixture); got != 443 {
		t.Fatalf("after inbound delete: exit_port = %d, want 443", got)
	}

	// Registration moves nodes.port and rewrites the direct chain in one
	// transaction; the chain write is what picks the new port up.
	exec(t, fixture, `UPDATE nodes SET port = 8443 WHERE id = $1`, fixture.exitID)
	exec(t, fixture, `UPDATE node_chains SET exit_port = 8443
		WHERE exit_node_id = $1 AND relay_pool_id IS NULL AND entry_node_id IS NULL`, fixture.exitID)
	if got := directExitPort(t, fixture); got != 8443 {
		t.Fatalf("after re-registration on 8443: exit_port = %d, want 8443", got)
	}
}

func TestDirectChainExitPort_leavesRelayedChainsAlone(t *testing.T) {
	fixture := newRelayPortFixture(t)
	if err := fixture.insertClaim(relayPortClaim{name: "relayed", poolID: fixture.poolIDs[0], port: 23120, transport: "tcp"}); err != nil {
		t.Fatalf("insert relayed chain: %v", err)
	}
	addInbound(t, fixture, 20100)
	var port int
	if err := fixture.pool.QueryRow(fixture.ctx,
		`SELECT exit_port FROM node_chains WHERE exit_node_id = $1 AND relay_pool_id IS NOT NULL`,
		fixture.exitID,
	).Scan(&port); err != nil {
		t.Fatalf("read relayed chain: %v", err)
	}
	// insertClaim targets 443; a relayed chain names its port explicitly.
	if port != 443 {
		t.Fatalf("relayed chain exit_port = %d, want its explicit 443", port)
	}
}

func TestDirectChainExitPort_migrationRepairsDriftAndNodeDeleteCascades(t *testing.T) {
	fixture := newRelayPortFixture(t)
	exec(t, fixture, `INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
		VALUES ('direct', $1, 443, 'tcp_udp')`, fixture.exitID)
	addInbound(t, fixture, 20100)

	// Recreate the pre-fix drift with the trigger out of the way.
	exec(t, fixture, `ALTER TABLE node_chains DISABLE TRIGGER node_chains_direct_exit_port`)
	exec(t, fixture, `UPDATE node_chains SET exit_port = 8443
		WHERE exit_node_id = $1 AND relay_pool_id IS NULL AND entry_node_id IS NULL`, fixture.exitID)
	exec(t, fixture, `ALTER TABLE node_chains ENABLE TRIGGER node_chains_direct_exit_port`)
	if got := directExitPort(t, fixture); got != 8443 {
		t.Fatalf("drift setup: exit_port = %d, want 8443", got)
	}

	if err := Migrate(fixture.ctx, fixture.pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := directExitPort(t, fixture); got != 20100 {
		t.Fatalf("after migration: exit_port = %d, want the inbound's 20100", got)
	}

	// Deleting the node cascades its inbounds and chains in one statement; the
	// inbound trigger must not touch a chain that statement is deleting.
	exec(t, fixture, `DELETE FROM nodes WHERE id = $1`, fixture.exitID)
}
