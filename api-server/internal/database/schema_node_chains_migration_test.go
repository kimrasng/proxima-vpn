package database

import "testing"

func Test_NodeChainsEntryPort_migration_aborts_without_changing_legacy_overlaps(t *testing.T) {
	fixture := newRelayPortFixture(t)
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(fixture.ctx, `DELETE FROM node_chains WHERE name LIKE 'legacy migration %'`)
		_, _ = fixture.pool.Exec(fixture.ctx, `DROP INDEX IF EXISTS idx_node_chains_entry_port`)
		_, _ = fixture.pool.Exec(fixture.ctx, `DROP INDEX IF EXISTS idx_node_chains_entry_port_both`)
		if err := Migrate(fixture.ctx, fixture.pool); err != nil {
			t.Errorf("restore relay entry-port indexes: %v", err)
		}
	})

	// Given
	if _, err := fixture.pool.Exec(fixture.ctx, `
		DROP INDEX IF EXISTS idx_node_chains_entry_port_tcp_claim;
		DROP INDEX IF EXISTS idx_node_chains_entry_port_udp_claim;
		CREATE UNIQUE INDEX idx_node_chains_entry_port
			ON node_chains(entry_port, transport) WHERE entry_port IS NOT NULL;
		CREATE UNIQUE INDEX idx_node_chains_entry_port_both
			ON node_chains(entry_port) WHERE entry_port IS NOT NULL AND transport = 'tcp_udp';
	`); err != nil {
		t.Fatalf("restore legacy indexes: %v", err)
	}

	tests := []struct {
		name         string
		port         int
		first        string
		second       string
		wantConflict string
	}{
		{name: "tcp claim", port: 23107, first: "tcp", second: "tcp_udp", wantConflict: "conflicting TCP claims"},
		{name: "udp claim", port: 23111, first: "udp", second: "tcp_udp", wantConflict: "conflicting UDP claims"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			firstName := "legacy migration " + test.name + " first"
			secondName := "legacy migration " + test.name + " second"
			if err := fixture.insertClaim(relayPortClaim{name: firstName, poolID: fixture.poolIDs[0], port: test.port, transport: test.first}); err != nil {
				t.Fatalf("insert legacy %s claim: %v", test.first, err)
			}
			if err := fixture.insertClaim(relayPortClaim{name: secondName, poolID: fixture.poolIDs[1], port: test.port, transport: test.second}); err != nil {
				t.Fatalf("insert legacy %s claim: %v", test.second, err)
			}

			// When
			err := Migrate(fixture.ctx, fixture.pool)

			// Then
			if err == nil {
				t.Fatalf("migration accepted legacy %s and %s claims on entry port %d", test.first, test.second, test.port)
			}
			if !contains(err.Error(), test.wantConflict) {
				t.Errorf("migration failed for the wrong reason: %v", err)
			}

			var claimCount int
			if err := fixture.pool.QueryRow(fixture.ctx,
				`SELECT COUNT(*) FROM node_chains WHERE name IN ($1, $2)`, firstName, secondName,
			).Scan(&claimCount); err != nil {
				t.Fatalf("count legacy claims: %v", err)
			}
			if claimCount != 2 {
				t.Errorf("migration changed legacy claims: got %d rows, want 2", claimCount)
			}

			var legacyIndexes int
			if err := fixture.pool.QueryRow(fixture.ctx, `
				SELECT COUNT(*) FROM pg_indexes
				WHERE tablename = 'node_chains'
				  AND indexname IN ('idx_node_chains_entry_port', 'idx_node_chains_entry_port_both')
			`).Scan(&legacyIndexes); err != nil {
				t.Fatalf("count legacy indexes: %v", err)
			}
			if legacyIndexes != 2 {
				t.Errorf("failed migration dropped legacy indexes: got %d, want 2", legacyIndexes)
			}

			if _, err := fixture.pool.Exec(fixture.ctx,
				`DELETE FROM node_chains WHERE name IN ($1, $2)`, firstName, secondName,
			); err != nil {
				t.Fatalf("delete legacy claims: %v", err)
			}
		})
	}
}

func Test_NodeChainsDirect_migration_aborts_without_changing_legacy_duplicates(t *testing.T) {
	fixture := newRelayPortFixture(t)
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(fixture.ctx, `DELETE FROM node_chains WHERE name LIKE 'legacy direct migration %'`)
		if err := Migrate(fixture.ctx, fixture.pool); err != nil {
			t.Errorf("restore direct-chain index: %v", err)
		}
	})

	// Given
	if _, err := fixture.pool.Exec(fixture.ctx, `DROP INDEX IF EXISTS idx_node_chains_one_direct_per_exit_v2`); err != nil {
		t.Fatalf("drop direct-chain index: %v", err)
	}
	if err := insertDirectChain(fixture, "legacy direct migration first"); err != nil {
		t.Fatalf("insert first legacy direct chain: %v", err)
	}
	if err := insertDirectChain(fixture, "legacy direct migration second"); err != nil {
		t.Fatalf("insert second legacy direct chain: %v", err)
	}

	// When
	err := Migrate(fixture.ctx, fixture.pool)

	// Then
	if err == nil {
		t.Fatal("migration accepted legacy duplicate direct chains")
	}
	if !contains(err.Error(), "duplicate direct chains") {
		t.Errorf("migration failed for the wrong reason: %v", err)
	}
	var directChains int
	if err := fixture.pool.QueryRow(fixture.ctx,
		`SELECT COUNT(*) FROM node_chains WHERE exit_node_id = $1 AND relay_pool_id IS NULL`, fixture.exitID,
	).Scan(&directChains); err != nil {
		t.Fatalf("count legacy direct chains: %v", err)
	}
	if directChains != 2 {
		t.Errorf("migration changed legacy direct chains: got %d rows, want 2", directChains)
	}
	var indexExists bool
	if err := fixture.pool.QueryRow(fixture.ctx,
		`SELECT to_regclass('idx_node_chains_one_direct_per_exit') IS NOT NULL`,
	).Scan(&indexExists); err != nil {
		t.Fatalf("check direct-chain index: %v", err)
	}
	if indexExists {
		t.Error("failed migration created the direct-chain unique index")
	}
}
