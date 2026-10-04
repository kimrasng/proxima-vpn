package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type relayPortFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	exitID  string
	poolIDs [2]string
}

type relayPortClaim struct {
	name      string
	poolID    string
	port      int
	transport string
}

func newRelayPortFixture(t *testing.T) relayPortFixture {
	t.Helper()
	pool := testDB(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	fixture := relayPortFixture{ctx: ctx, pool: pool}
	nameSeed := t.Name()
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, api_key, ip, port, role)
		 VALUES ($1, 'relay-port-test', '203.0.113.10'::inet, 443, 'exit')
		 RETURNING id::text`, nameSeed,
	).Scan(&fixture.exitID); err != nil {
		t.Fatalf("seed exit node: %v", err)
	}
	for index := range fixture.poolIDs {
		if err := pool.QueryRow(ctx,
			`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`,
			fmt.Sprintf("%s-pool-%d", nameSeed, index),
		).Scan(&fixture.poolIDs[index]); err != nil {
			t.Fatalf("seed relay pool %d: %v", index, err)
		}
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, fixture.exitID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = ANY($1::uuid[])`, fixture.poolIDs[:])
	})
	return fixture
}

func (fixture relayPortFixture) insertClaim(claim relayPortClaim) error {
	_, err := fixture.pool.Exec(fixture.ctx,
		`INSERT INTO node_chains
		 (name, relay_pool_id, entry_host, exit_node_id, exit_port, entry_port, transport)
		 VALUES ($1, $2, 'relay.example.test', $3, 443, $4, $5)`,
		claim.name, claim.poolID, fixture.exitID, claim.port, claim.transport,
	)
	return err
}

func Test_NodeChainsEntryPort_rejects_single_transport_overlap_with_tcp_udp_in_both_orders(t *testing.T) {
	fixture := newRelayPortFixture(t)
	tests := []struct {
		name       string
		port       int
		first      string
		second     string
		secondPool int
	}{
		{name: "tcp then tcp_udp", port: 23101, first: "tcp", second: "tcp_udp"},
		{name: "tcp_udp then tcp", port: 23102, first: "tcp_udp", second: "tcp"},
		{name: "udp then tcp_udp", port: 23103, first: "udp", second: "tcp_udp"},
		{name: "tcp_udp then udp", port: 23104, first: "tcp_udp", second: "udp"},
		{name: "tcp then tcp_udp across pools", port: 23112, first: "tcp", second: "tcp_udp", secondPool: 1},
		{name: "tcp_udp then tcp across pools", port: 23113, first: "tcp_udp", second: "tcp", secondPool: 1},
		{name: "udp then tcp_udp across pools", port: 23114, first: "udp", second: "tcp_udp", secondPool: 1},
		{name: "tcp_udp then udp across pools", port: 23115, first: "tcp_udp", second: "udp", secondPool: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			first := relayPortClaim{name: test.name + " first", poolID: fixture.poolIDs[0], port: test.port, transport: test.first}
			second := relayPortClaim{name: test.name + " second", poolID: fixture.poolIDs[test.secondPool], port: test.port, transport: test.second}
			if err := fixture.insertClaim(first); err != nil {
				t.Fatalf("insert first claim: %v", err)
			}

			// When
			err := fixture.insertClaim(second)

			// Then
			if err == nil {
				t.Errorf("%s accepted after %s claimed entry port %d", test.second, test.first, test.port)
			}
		})
	}
}

func Test_NodeChainsEntryPort_allows_tcp_and_udp_to_share_port(t *testing.T) {
	fixture := newRelayPortFixture(t)

	// Given
	if err := fixture.insertClaim(relayPortClaim{name: "tcp claim", poolID: fixture.poolIDs[0], port: 23105, transport: "tcp"}); err != nil {
		t.Fatalf("insert tcp claim: %v", err)
	}

	// When
	err := fixture.insertClaim(relayPortClaim{name: "udp claim", poolID: fixture.poolIDs[0], port: 23105, transport: "udp"})

	// Then
	if err != nil {
		t.Errorf("insert udp claim beside tcp claim: %v", err)
	}
}

func Test_NodeChainsEntryPort_rejects_same_transport_overlap(t *testing.T) {
	fixture := newRelayPortFixture(t)
	tests := []struct {
		name      string
		port      int
		transport string
	}{
		{name: "tcp", port: 23108, transport: "tcp"},
		{name: "udp", port: 23109, transport: "udp"},
		{name: "tcp_udp", port: 23110, transport: "tcp_udp"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			if err := fixture.insertClaim(relayPortClaim{name: test.name + " first", poolID: fixture.poolIDs[0], port: test.port, transport: test.transport}); err != nil {
				t.Fatalf("insert first %s claim: %v", test.transport, err)
			}

			// When
			err := fixture.insertClaim(relayPortClaim{name: test.name + " second", poolID: fixture.poolIDs[0], port: test.port, transport: test.transport})

			// Then
			if err == nil {
				t.Errorf("second %s claim accepted on entry port %d", test.transport, test.port)
			}
		})
	}
}

func Test_NodeChainsEntryPort_rejects_overlap_across_pools(t *testing.T) {
	fixture := newRelayPortFixture(t)

	// Given
	if err := fixture.insertClaim(relayPortClaim{name: "both claim", poolID: fixture.poolIDs[0], port: 23106, transport: "tcp_udp"}); err != nil {
		t.Fatalf("insert tcp_udp claim: %v", err)
	}

	// When
	err := fixture.insertClaim(relayPortClaim{name: "cross-pool udp claim", poolID: fixture.poolIDs[1], port: 23106, transport: "udp"})

	// Then
	if err == nil {
		t.Error("udp claim in a second pool overlapped a fleet-wide tcp_udp claim")
	}
}

func Test_NodeChainsEntryPort_rejects_concurrent_tcp_and_tcp_udp_claims(t *testing.T) {
	fixture := newRelayPortFixture(t)
	ctx, cancel := context.WithTimeout(fixture.ctx, 5*time.Second)
	defer cancel()
	fixture.ctx = ctx

	// Given
	claims := [2]relayPortClaim{
		{name: "concurrent tcp claim", poolID: fixture.poolIDs[0], port: 23108, transport: "tcp"},
		{name: "concurrent both claim", poolID: fixture.poolIDs[1], port: 23108, transport: "tcp_udp"},
	}
	ready := make(chan struct{}, len(claims))
	start := make(chan struct{})
	results := make(chan error, len(claims))
	for _, claim := range claims {
		go func() {
			ready <- struct{}{}
			<-start
			results <- fixture.insertClaim(claim)
		}()
	}
	for range claims {
		<-ready
	}

	// When
	close(start)
	insertErrors := [2]error{<-results, <-results}

	// Then
	successes := 0
	uniqueViolations := 0
	for _, err := range insertErrors {
		if err == nil {
			successes++
			continue
		}
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			uniqueViolations++
		}
	}
	if successes != 1 || uniqueViolations != 1 {
		t.Fatalf("concurrent claims produced %d successes and %d unique violations; errors: %v", successes, uniqueViolations, insertErrors)
	}
}

func Test_NodeChains_rejects_blank_entry_host_for_relayed_chain(t *testing.T) {
	fixture := newRelayPortFixture(t)

	_, err := fixture.pool.Exec(fixture.ctx,
		`INSERT INTO node_chains
		 (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ('blank relay host', $1, '   ', 23118, $2, 443, 'tcp')`,
		fixture.poolIDs[0], fixture.exitID,
	)

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" || postgresError.ConstraintName != "chain_relay_entry_host" {
		t.Fatalf("blank relayed entry host error = %v, want named check violation", err)
	}
}
