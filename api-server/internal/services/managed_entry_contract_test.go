package services

import (
	"context"
	"errors"
	"testing"
)

func TestPHS028EntryReturnsOnlyStoredHostname(t *testing.T) {
	// Given a live managed hostname distinct from the node IP.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	if _, err := pool.Exec(t.Context(), `INSERT INTO managed_entry_dns (owner_node_id,node_id,hostname) VALUES ($1,$1,'entry.example.test')`, node); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, node); err != nil {
			t.Error(err)
		}
	})
	// When a handler reads by live node ID.
	got, err := ManagedEntryHostname(t.Context(), pool, node)
	// Then it receives only the stored hostname.
	if err != nil || got != "entry.example.test" {
		t.Fatalf("hostname=%q error=%v", got, err)
	}
}

func TestPHS028EntryUnavailableNeverFallsBack(t *testing.T) {
	// Given a node with nullable managed hostname and a node with no association.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	other := phs028ServiceNode(t, pool)
	if _, err := pool.Exec(t.Context(), `INSERT INTO managed_entry_dns (owner_node_id,node_id) VALUES ($1,$1)`, node); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, node); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []string{node, other} {
		// When querying the live ID.
		got, err := ManagedEntryHostname(t.Context(), pool, id)
		// Then no node IP or legacy host is substituted.
		if got != "" || !errors.Is(err, ErrManagedEntryUnavailable) {
			t.Fatalf("hostname=%q error=%v", got, err)
		}
		var semantic *RealitySNIError
		if !errors.As(err, &semantic) || semantic.Kind != RealitySNIEntryUnavailable {
			t.Fatalf("missing typed Entry error: %v", err)
		}
	}
}

func TestPHS028EntryPropagatesDatabaseFailure(t *testing.T) {
	// Given an unavailable pool.
	pool := phs028ServiceDB(t)
	pool.Close()
	// When querying a live node ID.
	_, err := ManagedEntryHostname(t.Context(), pool, "00000000-0000-0000-0000-000000000001")
	// Then the DB failure is distinct from hostname unavailability.
	if err == nil || errors.Is(err, ErrManagedEntryUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestPHS028EntryTransactionSeesUncommittedHostnameOnlyInsideItsOwnTransaction(t *testing.T) {
	// Given a managed hostname inserted but not committed in transaction A.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	a := phs028ServiceTx(t, pool)
	b := phs028ServiceTx(t, pool)
	if _, err := a.Exec(t.Context(), `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname) VALUES ($1, $1, 'pending.example.test')`, node); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, node); err != nil {
			t.Error(err)
		}
	})

	// When the same transaction reads its uncommitted hostname.
	got, err := ManagedEntryHostname(t.Context(), a, node)
	// Then it sees the stored value before commit.
	if err != nil || got != "pending.example.test" {
		t.Fatalf("transaction A hostname=%q error=%v", got, err)
	}
	// When the pool and independently started transaction B query before commit.
	got, err = ManagedEntryHostname(t.Context(), pool, node)
	if got != "" || !errors.Is(err, ErrManagedEntryUnavailable) {
		t.Fatalf("pool before commit: hostname=%q error=%v", got, err)
	}
	got, err = ManagedEntryHostname(t.Context(), b, node)
	// Then neither sees the uncommitted value or a fallback.
	if got != "" || !errors.Is(err, ErrManagedEntryUnavailable) {
		t.Fatalf("transaction B before commit: hostname=%q error=%v", got, err)
	}
	if err := a.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	// When both readers query after commit.
	got, err = ManagedEntryHostname(t.Context(), pool, node)
	if err != nil || got != "pending.example.test" {
		t.Fatalf("pool after commit: hostname=%q error=%v", got, err)
	}
	got, err = ManagedEntryHostname(t.Context(), b, node)
	// Then both see precisely the committed hostname.
	if err != nil || got != "pending.example.test" {
		t.Fatalf("transaction B after commit: hostname=%q error=%v", got, err)
	}
}

func TestPHS028EntryTransactionDoesNotUseNodeIPOrLegacyChainHostForNullOrDetachedRows(t *testing.T) {
	// Given a NULL live hostname, a detached owner, and a legacy chain hostname.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	exit := phs028ServiceNode(t, pool)
	detached := phs028ServiceNode(t, pool)
	if _, err := pool.Exec(t.Context(), `INSERT INTO managed_entry_dns (owner_node_id, node_id) VALUES ($1, $1), ($2, NULL)`, node, detached); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id IN ($1, $2)`, node, detached); err != nil {
			t.Error(err)
		}
	})
	var chainID string
	if err := pool.QueryRow(t.Context(), `INSERT INTO node_chains (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport) VALUES ($1, $2, 'legacy.example.test', 24001, $3, 443, 'tcp') RETURNING id::text`, t.Name(), node, exit).Scan(&chainID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM node_chains WHERE id=$1`, chainID); err != nil {
			t.Error(err)
		}
	})
	tx := phs028ServiceTx(t, pool)
	// When the transaction reads by the live node and detached owner's node ID.
	for _, id := range []string{node, detached} {
		got, err := ManagedEntryHostname(t.Context(), tx, id)
		// Then both are typed unavailable, never node IP or chain host.
		var semantic *RealitySNIError
		if got != "" || !errors.Is(err, ErrManagedEntryUnavailable) || !errors.As(err, &semantic) || semantic.Kind != RealitySNIEntryUnavailable {
			t.Fatalf("node=%s hostname=%q error=%v", id, got, err)
		}
	}
}
