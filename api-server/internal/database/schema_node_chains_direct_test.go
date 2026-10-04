package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const directChainIndexName = "idx_node_chains_one_direct_per_exit_v2"

func insertDirectChain(fixture relayPortFixture, name string) error {
	_, err := fixture.pool.Exec(fixture.ctx,
		`INSERT INTO node_chains (name, exit_node_id, exit_port, entry_port, transport)
		 VALUES ($1, $2, 443, NULL, 'tcp_udp')`, name, fixture.exitID,
	)
	return err
}

func Test_NodeChainsDirect_rejects_second_chain_for_same_exit(t *testing.T) {
	fixture := newRelayPortFixture(t)

	// Given
	if err := insertDirectChain(fixture, "direct one"); err != nil {
		t.Fatalf("insert first direct chain: %v", err)
	}

	// When
	err := insertDirectChain(fixture, "direct two")

	// Then
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" || postgresError.ConstraintName != directChainIndexName {
		t.Fatalf("second direct chain error = %v, want named unique violation from %s", err, directChainIndexName)
	}
}

func Test_NodeChainsDirect_allows_multiple_relayed_chains_for_same_exit(t *testing.T) {
	fixture := newRelayPortFixture(t)

	// Given
	if err := fixture.insertClaim(relayPortClaim{name: "relay one", poolID: fixture.poolIDs[0], port: 23116, transport: "tcp"}); err != nil {
		t.Fatalf("insert first relayed chain: %v", err)
	}

	// When
	err := fixture.insertClaim(relayPortClaim{name: "relay two", poolID: fixture.poolIDs[1], port: 23117, transport: "tcp"})

	// Then
	if err != nil {
		t.Fatalf("insert second relayed chain for one exit: %v", err)
	}
}

func Test_NodeChainsDirect_allows_explicit_link_beside_direct_chain(t *testing.T) {
	fixture := newRelayPortFixture(t)
	var entryID string
	if err := fixture.pool.QueryRow(fixture.ctx,
		`INSERT INTO nodes (name, api_key, ip, port, role)
		 VALUES ('explicit entry', 'test', '203.0.113.11'::inet, 443, 'relay') RETURNING id::text`,
	).Scan(&entryID); err != nil {
		t.Fatalf("seed entry node: %v", err)
	}

	if err := insertDirectChain(fixture, "direct"); err != nil {
		t.Fatalf("insert direct chain: %v", err)
	}
	_, err := fixture.pool.Exec(fixture.ctx,
		`INSERT INTO node_chains
		 (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ('explicit', $1, '203.0.113.11', 23119, $2, 443, 'tcp')`, entryID, fixture.exitID,
	)
	if err != nil {
		t.Fatalf("insert explicit link beside direct chain: %v", err)
	}
}

func Test_NodeChainsDirect_rejects_concurrent_duplicates(t *testing.T) {
	fixture := newRelayPortFixture(t)
	ctx, cancel := context.WithTimeout(fixture.ctx, 5*time.Second)
	defer cancel()
	fixture.ctx = ctx

	// Given
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"concurrent direct one", "concurrent direct two"} {
		go func() {
			ready <- struct{}{}
			<-start
			results <- insertDirectChain(fixture, name)
		}()
	}
	<-ready
	<-ready

	// When
	close(start)
	insertErrors := [2]error{<-results, <-results}

	// Then
	successes := 0
	namedUniqueViolations := 0
	for _, err := range insertErrors {
		if err == nil {
			successes++
			continue
		}
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == directChainIndexName {
			namedUniqueViolations++
		}
	}
	if successes != 1 || namedUniqueViolations != 1 {
		t.Fatalf("concurrent direct inserts produced %d successes and %d named unique violations; errors: %v", successes, namedUniqueViolations, insertErrors)
	}
}
