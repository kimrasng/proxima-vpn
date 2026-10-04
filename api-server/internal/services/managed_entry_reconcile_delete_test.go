package services

import (
	"errors"
	"net/http"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/models"
)

func TestReconcileDelete_removesOnlyOwnedRecords(t *testing.T) {
	// Given owned and foreign records at the same name.
	f := newReconcileFixture(t)
	s := f.state(t)
	f.seed(t, "A", s.marker, s.ipv4)
	f.seed(t, "A", s.marker, s.ipv4)
	foreign := f.seed(t, "CNAME", "other", "target.example.test")
	f.setDelete(t)
	// When
	if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	// Then
	assertReconcileState(t, f, models.ManagedDNSDeleted, "", 1)
	if f.records()[0].ID != foreign {
		t.Fatalf("foreign deleted: %+v", f.records())
	}
}

func TestReconcileDelete_lostDeleteAndAlreadyAbsent(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{true: "absent", false: "lost"}[absent], func(t *testing.T) {
			// Given an already absent or response-lost deletion.
			f := newReconcileFixture(t)
			if !absent {
				s := f.state(t)
				f.seed(t, "A", s.marker, s.ipv4)
				f.provider.faultMethod, f.provider.faultStatus, f.provider.lost = http.MethodDelete, 503, true
			}
			f.setDelete(t)
			// When
			if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
				t.Fatal(err)
			}
			// Then
			assertReconcileState(t, f, models.ManagedDNSDeleted, "", 0)
		})
	}
}

func TestReconcileDelete_ownedRecordWithoutIDIsNotAbsence(t *testing.T) {
	// Given a malformed exact-name owned A record that cannot be targeted.
	f := newReconcileFixture(t)
	s := f.state(t)
	f.provider.records["missing-id"] = CloudflareDNSRecord{Name: s.hostname, Type: "A", Comment: s.marker}
	f.setDelete(t)
	// When
	if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	// Then
	assertReconcileState(t, f, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, 1)
}

func TestReconcilePresent_rejectsInvalidFrozenConfigWithoutProviderCalls(t *testing.T) {
	for _, zone := range []bool{false, true} {
		t.Run(map[bool]string{true: "zone", false: "base"}[zone], func(t *testing.T) {
			// Given a live config that differs from the frozen binding.
			f := newReconcileFixture(t)
			cfg := entryConfig
			if zone {
				cfg.ZoneID = "abcdef0123456789abcdef0123456789"
			} else {
				cfg.BaseDomain = "other.example.test"
			}
			f.reconciler.deps.Intent = cfg
			// When
			if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
				t.Fatal(err)
			}
			// Then
			assertReconcileState(t, f, models.ManagedDNSError, models.ManagedDNSInvalidConfiguration, 0)
			if f.provider.requests != 0 {
				t.Fatalf("provider calls=%d", f.provider.requests)
			}
		})
	}
}

func TestReconcilePresent_readyTransientPreservesObservation(t *testing.T) {
	// Given a ready observation and transient discovery failure.
	f := newReconcileFixture(t)
	s := f.state(t)
	f.seed(t, "A", s.marker, s.ipv4)
	if _, err := f.pool.Exec(t.Context(), `UPDATE managed_entry_dns SET dns_status='ready',observed_ipv4=desired_ipv4 WHERE owner_node_id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	f.provider.faultMethod, f.provider.faultStatus = http.MethodGet, 503
	// When
	if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	// Then
	assertReconcileState(t, f, models.ManagedDNSReady, models.ManagedDNSProviderUnavailable, 1)
	var observed string
	if err := f.pool.QueryRow(t.Context(), `SELECT host(observed_ipv4) FROM managed_entry_dns WHERE owner_node_id=$1`, f.owner).Scan(&observed); err != nil || observed != s.ipv4 {
		t.Fatalf("observed=%s %v", observed, err)
	}
}

func TestReconcilePresent_duplicateWorkerLockContention(t *testing.T) {
	// Given a first worker blocked inside POST while holding the owner session.
	f := newReconcileFixture(t)
	f.provider.started, f.provider.resume = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- f.reconciler.ReconcileOwner(t.Context(), f.owner) }()
	<-f.provider.started
	// When a second worker attempts the same owner.
	if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	close(f.provider.resume)
	// Then
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertReconcileState(t, f, models.ManagedDNSReady, "", 1)
}

func TestReconcilePresent_deletionIntentWhilePostDelayed(t *testing.T) {
	// Given a delayed POST and a concurrent delete intent.
	f := newReconcileFixture(t)
	f.provider.started, f.provider.resume = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- f.reconciler.ReconcileOwner(t.Context(), f.owner) }()
	<-f.provider.started
	f.setDelete(t)
	// When POST completes, its old action cannot become Ready.
	close(f.provider.resume)
	// Then
	if err := <-done; !errors.Is(err, ErrManagedEntryDNSStale) {
		t.Fatalf("stale=%v", err)
	}
	if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	assertReconcileState(t, f, models.ManagedDNSDeleted, "", 0)
}
