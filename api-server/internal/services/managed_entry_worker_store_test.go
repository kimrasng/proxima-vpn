package services

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/models"
)

func TestManagedEntryWorkerDueOrderingAndLimit(t *testing.T) {
	// Given two due entries and one future entry.
	pool := phs028ServiceDB(t)
	owners := []string{entryNode(t, pool, "relay"), entryNode(t, pool, "relay"), entryNode(t, pool, "relay")}
	for _, owner := range owners {
		ensureEntry(t, pool, owner, entryConfig)
	}
	for i, owner := range owners {
		_, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET next_attempt_at=NOW() + $2::interval WHERE owner_node_id=$1`, owner, []string{"-2 hours", "-1 hour", "1 hour"}[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	// When listing due work with a bounded limit.
	store := NewManagedEntryDNSWorkerStore(pool)
	first, err := store.ListDue(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.ListDue(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	// Then only due rows are ordered and scanning has not locked their rows.
	if len(first) != 1 || len(all) != 2 || all[0].OwnerNodeID != owners[0] || all[1].OwnerNodeID != owners[1] || first[0].ID != all[0].ID {
		t.Fatalf("due=%+v first=%+v", all, first)
	}
	tx := phs028ServiceTx(t, pool)
	if _, err := tx.Exec(t.Context(), `UPDATE managed_entry_dns SET updated_at=NOW() WHERE id=$1`, all[0].ID); err != nil {
		t.Fatalf("due list retained row lock: %v", err)
	}
	if _, err := store.ListDue(t.Context(), 0); err == nil {
		t.Fatal("zero limit accepted")
	}
}

func TestManagedEntryWorkerReadyAndDeletedCAS(t *testing.T) {
	// Given a live intent snapshot held through provider work, with no DB transaction.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	store := NewManagedEntryDNSWorkerStore(pool)
	if _, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET retry_count=4,error_code='provider_unavailable' WHERE owner_node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	a, err := store.TryAcquire(t.Context(), node)
	if err != nil || a == nil {
		t.Fatalf("acquire: %v", err)
	}
	defer a.Release(context.Background())
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	// When provider success is persisted using the snapshot generation and action.
	if err := a.Ready(t.Context(), netip.MustParseAddr("203.0.113.10"), "record-1", now); err != nil {
		t.Fatal(err)
	}
	var status, observed, record string
	var next time.Time
	var code *string
	var retries int32
	if err := pool.QueryRow(t.Context(), `SELECT dns_status,host(observed_ipv4),provider_record_id,next_attempt_at,error_code,retry_count FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&status, &observed, &record, &next, &code, &retries); err != nil {
		t.Fatal(err)
	}
	// Then observation and the five-minute audit are durable; a generation race is stale.
	if status != "ready" || observed != "203.0.113.10" || record != "record-1" || !next.Equal(now.Add(5*time.Minute)) || code != nil || retries != 0 {
		t.Fatalf("ready=%s %s %s %s", status, observed, record, next)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET generation=generation+1,desired_action='delete',cleanup_requested_at=NOW() WHERE owner_node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	if err := a.Ready(t.Context(), netip.MustParseAddr("203.0.113.10"), "record-2", now); !errors.Is(err, ErrManagedEntryDNSStale) {
		t.Fatalf("stale ready: %v", err)
	}
	if err := a.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET retry_count=2,error_code='provider_unavailable' WHERE owner_node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	b, err := store.TryAcquire(t.Context(), node)
	if err != nil || b == nil {
		t.Fatalf("reacquire: %v", err)
	}
	defer b.Release(context.Background())
	if err := b.Deleted(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	var live *string
	var cleanup time.Time
	if err := pool.QueryRow(t.Context(), `SELECT dns_status,node_id::text,cleanup_requested_at,next_attempt_at,provider_record_id,error_code,retry_count FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&status, &live, &cleanup, &next, &record, &code, &retries); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" || live == nil || cleanup.IsZero() || record != "record-1" || !next.Equal(now.Add(5*time.Minute)) || code != nil || retries != 0 {
		t.Fatalf("deleted=%s %v %s %s", status, live, record, next)
	}
	var observedAfter *string
	if err := pool.QueryRow(t.Context(), `SELECT host(observed_ipv4) FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&observedAfter); err != nil {
		t.Fatal(err)
	}
	if observedAfter != nil {
		t.Fatalf("deleted observation: %s", *observedAfter)
	}
}

func TestManagedEntryWorkerFailureAndActionCAS(t *testing.T) {
	// Given an acquired present intent and a deterministic audit time.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	a, err := NewManagedEntryDNSWorkerStore(pool).TryAcquire(t.Context(), node)
	if err != nil || a == nil {
		t.Fatalf("acquire: %v", err)
	}
	defer a.Release(context.Background())
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	// When a durable conflict is recorded, its retry schedule can be disabled.
	if err := a.Failed(t.Context(), models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, now, nil); err != nil {
		t.Fatal(err)
	}
	var status, code string
	var next *time.Time
	if err := pool.QueryRow(t.Context(), `SELECT dns_status,error_code,next_attempt_at FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&status, &code, &next); err != nil {
		t.Fatal(err)
	}
	if status != "conflict" || code != "ownership_conflict" || next != nil {
		t.Fatalf("failure=%s %s %v", status, code, next)
	}
	if err := a.Failed(t.Context(), models.ManagedDNSPending, models.ManagedDNSOwnershipConflict, now, nil); err == nil {
		t.Fatal("accepted non-failure status")
	}
	if err := a.Failed(t.Context(), models.ManagedDNSError, "arbitrary", now, nil); err == nil {
		t.Fatal("accepted unlisted error")
	}
	// Then changing action alone makes the old snapshot stale even without generation movement.
	if _, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET desired_action='delete' WHERE owner_node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	if err := a.Failed(t.Context(), models.ManagedDNSError, models.ManagedDNSProviderRejected, now, nil); !errors.Is(err, ErrManagedEntryDNSStale) {
		t.Fatalf("action CAS: %v", err)
	}
}

func TestManagedEntryWorkerTransientPendingAndDeleting(t *testing.T) {
	for _, action := range []models.ManagedDNSAction{models.ManagedDNSPresent, models.ManagedDNSDelete} {
		t.Run(string(action), func(t *testing.T) {
			// Given an intent that is not last-known-good at its current desired address.
			pool := phs028ServiceDB(t)
			node := entryNode(t, pool, "relay")
			ensureEntry(t, pool, node, entryConfig)
			if _, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET desired_action=$2,dns_status='ready',observed_ipv4='203.0.113.99' WHERE owner_node_id=$1`, node, action); err != nil {
				t.Fatal(err)
			}
			a, err := NewManagedEntryDNSWorkerStore(pool).TryAcquire(t.Context(), node)
			if err != nil || a == nil {
				t.Fatalf("acquire: %v", err)
			}
			defer a.Release(context.Background())
			// When persisting a transient failure without a Retry-After minimum.
			if err := a.Transient(t.Context(), models.ManagedDNSProviderUnavailable, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), 0, 0); err != nil {
				t.Fatal(err)
			}
			// Then present returns to pending while a tombstone stays deleting.
			var status models.ManagedDNSStatus
			if err := pool.QueryRow(t.Context(), `SELECT dns_status FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&status); err != nil {
				t.Fatal(err)
			}
			want := models.ManagedDNSPending
			if action == models.ManagedDNSDelete {
				want = models.ManagedDNSDeleting
			}
			if status != want {
				t.Fatalf("status=%s want %s", status, want)
			}
		})
	}
}

func TestManagedEntryWorkerTransientSurvivesStoreRecreation(t *testing.T) {
	// Given a ready row whose desired address has not changed.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	_, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET dns_status='ready',observed_ipv4=desired_ipv4 WHERE owner_node_id=$1`, node)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewManagedEntryDNSWorkerStore(pool).TryAcquire(t.Context(), node)
	if err != nil || a == nil {
		t.Fatalf("acquire: %v", err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	// When persisting a transient provider failure.
	if err := a.Transient(t.Context(), models.ManagedDNSProviderUnavailable, now, 7*time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then a new store observes the same last-known-good status and durable retry time.
	b, err := NewManagedEntryDNSWorkerStore(pool).TryAcquire(t.Context(), node)
	if err != nil || b == nil {
		t.Fatalf("reacquire: %v", err)
	}
	defer b.Release(context.Background())
	s := b.Snapshot()
	if s.DNSStatus == nil || *s.DNSStatus != models.ManagedDNSReady || s.RetryCount != 1 || s.NextAttemptAt == nil || !s.NextAttemptAt.Equal(now.Add(7*time.Minute)) {
		t.Fatalf("retry=%+v", s)
	}
}
