package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestManagedEntryDeletionTombstonesOnceAndSurvivesNodeDelete(t *testing.T) {
	// Given a managed entry with provider identity.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	before := readEntry(t, pool, node)
	if _, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET provider_record_id='cf-record', dns_status='ready', next_attempt_at=NULL WHERE owner_node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	// When requesting cleanup twice under a locked node.
	for range 2 {
		tx := phs028ServiceTx(t, pool)
		locked, err := LockRealityNode(t.Context(), tx, node)
		if err != nil {
			t.Fatal(err)
		}
		if err := RequestManagedEntryDNSDeletion(t.Context(), tx, locked); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var requested bool
	if err := pool.QueryRow(t.Context(), `SELECT cleanup_requested_at IS NOT NULL FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	s := readEntry(t, pool, node)
	if !requested || s.action != "delete" || s.status != "deleting" || s.generation != before.generation+1 || !s.queued || s.hostname != before.hostname || s.marker != before.marker || s.zone != before.zone || s.recordID == nil || *s.recordID != "cf-record" {
		t.Fatalf("tombstone=%+v requested=%t", s, requested)
	}
	// Then explicit node deletion detaches but preserves the tombstone.
	name, err := DeleteNodeWithManagedDNS(t.Context(), pool, node)
	if err != nil || name != t.Name() {
		t.Fatalf("delete name=%q err=%v", name, err)
	}
	s = readEntry(t, pool, node)
	var detached bool
	if err := pool.QueryRow(t.Context(), `SELECT node_id IS NULL FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&detached); err != nil {
		t.Fatal(err)
	}
	if !detached || s.generation != before.generation+1 || s.hostname != before.hostname || s.recordID == nil || *s.recordID != "cf-record" {
		t.Fatalf("detached=%t state=%+v", detached, s)
	}
}

func TestManagedEntryDeletionRollbackOnForeignKeyConflict(t *testing.T) {
	// Given a node referenced by a non-cascading chain.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	exit := entryNode(t, pool, "exit")
	ensureEntry(t, pool, node, entryConfig)
	before := readEntry(t, pool, node)
	var chain string
	if err := pool.QueryRow(t.Context(), `INSERT INTO node_chains (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport) VALUES ($1,$2,'entry.example.test',29998,$3,443,'tcp') RETURNING id::text`, t.Name(), node, exit).Scan(&chain); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM node_chains WHERE id=$1`, chain); err != nil {
			t.Error(err)
		}
	})
	// When deletion encounters the FK, it returns the original pg error.
	_, err := DeleteNodeWithManagedDNS(t.Context(), pool, node)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("delete err=%v", err)
	}
	// Then the tombstone transition was rolled back with the DELETE.
	after := readEntry(t, pool, node)
	if after.action != "present" || after.generation != before.generation || after.hostname != before.hostname {
		t.Fatalf("after=%+v before=%+v", after, before)
	}
}

func TestManagedEntryIntentCannotResurrectDeletion(t *testing.T) {
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequestManagedEntryDNSDeletion(t.Context(), tx, locked); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := readEntry(t, pool, node)
	// When the node re-registers, its tombstone stays delete/deleting.
	ensureEntry(t, pool, node, entryConfig)
	after := readEntry(t, pool, node)
	if after.action != "delete" || after.status != "deleting" || after.generation != before.generation || after.hostname != before.hostname {
		t.Fatalf("after=%+v", after)
	}
}

func TestManagedEntryConcurrentRefreshAllocatesOneHostname(t *testing.T) {
	// Given two refreshes racing for the same node lock.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	first := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), first, node)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		second, err := LockRealityNode(t.Context(), tx, node)
		if err == nil {
			err = EnsureManagedEntryDNSIntent(t.Context(), tx, second, entryConfig)
		}
		if err == nil {
			err = tx.Commit(t.Context())
		}
		done <- err
	}()
	// When the first commits, the second sees the committed hostname.
	if err := EnsureManagedEntryDNSIntent(t.Context(), first, locked, entryConfig); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || readEntry(t, pool, node).generation != 1 {
		t.Fatalf("rows=%d", count)
	}
}

func TestManagedEntryOwnerLockSerializesSameImmutableID(t *testing.T) {
	// Given two transactions targeting the same immutable owner, the second cannot acquire the lock.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	a, b := phs028ServiceTx(t, pool), phs028ServiceTx(t, pool)
	if err := LockManagedEntryDNSOwner(t.Context(), a, node); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := LockManagedEntryDNSOwner(ctx, b, node); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("advisory lock err=%v", err)
	}
}

func TestManagedEntryDeletionMissingNodeKeepsSemanticError(t *testing.T) {
	pool := phs028ServiceDB(t)
	_, err := DeleteNodeWithManagedDNS(t.Context(), pool, "00000000-0000-0000-0000-000000000001")
	phs028Kind(t, err, RealitySNIMissingNode)
}
