package services

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagedEntryWorkerSessionLockConflictsAndReleases(t *testing.T) {
	// Given two independent owner rows and a session holding the first owner.
	pool := phs028ServiceDB(t)
	first := entryNode(t, pool, "relay")
	second := entryNode(t, pool, "relay")
	ensureEntry(t, pool, first, entryConfig)
	ensureEntry(t, pool, second, entryConfig)
	store := NewManagedEntryDNSWorkerStore(pool)
	a, err := store.TryAcquire(t.Context(), first)
	if err != nil || a == nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer a.Release(context.Background())
	// When a second session competes, while another owner is independent.
	b, err := store.TryAcquire(t.Context(), first)
	if err != nil || b != nil {
		t.Fatalf("same owner: %v %v", b, err)
	}
	c, err := store.TryAcquire(t.Context(), second)
	if err != nil || c == nil {
		t.Fatalf("other owner: %v", err)
	}
	defer c.Release(context.Background())
	// Then releasing the owner returns its lock independently of the caller's cancellation.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.Release(ctx); err != nil {
		t.Fatalf("release after cancellation: %v", err)
	}
	if err := a.Release(t.Context()); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	d, err := store.TryAcquire(t.Context(), first)
	if err != nil || d == nil {
		t.Fatalf("reacquire: %v", err)
	}
	defer d.Release(context.Background())
	// When explicit deletion targets that owner, its transaction lock must wait.
	tx := phs028ServiceTx(t, pool)
	deadline, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer stop()
	if err := LockManagedEntryDNSOwner(deadline, tx, first); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deletion lock: %v", err)
	}
	_ = tx.Rollback(context.Background())
}

func TestManagedEntryWorkerSnapshotIncludesNullableAndDurableFields(t *testing.T) {
	// Given a persisted ready row with provider identity and observation.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	_, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET provider_record_id='provider-1',observed_ipv4='203.0.113.10',observed_at=NOW(),dns_status='ready',retry_count=3,cleanup_requested_at=NOW() WHERE owner_node_id=$1`, node)
	if err != nil {
		t.Fatal(err)
	}
	// When acquiring the owner session.
	a, err := NewManagedEntryDNSWorkerStore(pool).TryAcquire(t.Context(), node)
	if err != nil || a == nil {
		t.Fatalf("acquire: %v", err)
	}
	defer a.Release(context.Background())
	s := a.Snapshot()
	// Then all provider inputs and durable history are captured together.
	if s.ID == "" || s.OwnerNodeID != node || s.NodeID == nil || *s.NodeID != node || s.Hostname == nil || s.CloudflareZoneID == nil || s.OwnershipMarker == nil || s.ProviderRecordID == nil || *s.ProviderRecordID != "provider-1" || s.DesiredIPv4 == nil || s.ObservedIPv4 == nil || s.ObservedAt == nil || s.DNSStatus == nil || *s.DNSStatus != "ready" || s.DesiredAction != "present" || s.ErrorCode != nil || s.Generation != 1 || s.RetryCount != 3 || s.CleanupRequestedAt == nil || s.NextAttemptAt == nil {
		t.Fatalf("incomplete snapshot: %+v", s)
	}
	*s.ProviderRecordID = "mutated"
	if *a.Snapshot().ProviderRecordID != "provider-1" {
		t.Fatal("snapshot returned mutable internal provider identity")
	}
}

func TestManagedEntryWorkerReleaseDiscardsBrokenSession(t *testing.T) {
	// Given a held session whose Postgres backend has disappeared.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	store := NewManagedEntryDNSWorkerStore(pool)
	a, err := store.TryAcquire(t.Context(), node)
	if err != nil || a == nil {
		t.Fatalf("acquire: %v", err)
	}
	var stopped bool
	if err := pool.QueryRow(t.Context(), `SELECT pg_terminate_backend($1)`, a.conn.Conn().PgConn().PID()).Scan(&stopped); err != nil || !stopped {
		t.Fatalf("terminate backend: %t %v", stopped, err)
	}
	// When releasing, the broken connection must not be returned to the pool.
	if err := a.Release(t.Context()); err == nil {
		t.Fatal("expected unlock error on terminated backend")
	}
	// Then release remains idempotent and a new session can own the same key.
	if err := a.Release(t.Context()); err != nil {
		t.Fatalf("second release: %v", err)
	}
	b, err := store.TryAcquire(t.Context(), node)
	if err != nil || b == nil {
		t.Fatalf("reacquire after failed unlock: %v", err)
	}
	defer b.Release(context.Background())
}
