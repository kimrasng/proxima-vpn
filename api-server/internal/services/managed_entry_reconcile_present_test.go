package services

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/models"
)

func TestReconcilePresent_createAndReady_whenEmpty(t *testing.T) {
	// Given an empty provider and due intent.
	f := newReconcileFixture(t)
	// When
	if err := f.reconciler.ReconcileDue(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	// Then
	assertReconcileState(t, f, models.ManagedDNSReady, "", 1)
	r := f.records()[0]
	s := f.state(t)
	if r.Name != s.hostname || r.Type != "A" || r.Comment != s.marker || r.Content != s.ipv4 || r.TTL != 300 || r.Proxied || s.recordID == nil || *s.recordID != r.ID {
		t.Fatalf("record=%+v state=%+v", r, s)
	}
	var next time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT next_attempt_at FROM managed_entry_dns WHERE owner_node_id=$1`, f.owner).Scan(&next); err != nil || !next.Equal(reconcileNow.Add(5*time.Minute)) {
		t.Fatalf("audit=%v %v", next, err)
	}
}

func TestReconcilePresent_convergesDriftAndDuplicates(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{true: "duplicate", false: "drift"}[duplicate], func(t *testing.T) {
			// Given a cached owned record with drift and optionally another owned record.
			f := newReconcileFixture(t)
			s := f.state(t)
			id := f.seed(t, "A", s.marker, "192.0.2.8")
			if duplicate {
				f.seed(t, "A", s.marker, "192.0.2.9")
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE managed_entry_dns SET provider_record_id=$2 WHERE owner_node_id=$1`, f.owner, id); err != nil {
				t.Fatal(err)
			}
			// When
			if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
				t.Fatal(err)
			}
			// Then
			assertReconcileState(t, f, models.ManagedDNSReady, "", 1)
			r := f.records()[0]
			if r.ID != id || r.Content != s.ipv4 {
				t.Fatalf("keeper=%+v", r)
			}
		})
	}
}

func TestReconcilePresent_lostWriteResponsesConverge(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			// Given a write that applies but whose response is lost.
			f := newReconcileFixture(t)
			if method == http.MethodPut {
				s := f.state(t)
				f.seed(t, "A", s.marker, "192.0.2.9")
			}
			f.provider.faultMethod, f.provider.faultStatus, f.provider.lost = method, 503, true
			// When
			if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
				t.Fatal(err)
			}
			// Then
			assertReconcileState(t, f, models.ManagedDNSReady, "", 1)
			if f.records()[0].Content != f.state(t).ipv4 {
				t.Fatalf("lost %s: %+v", method, f.records())
			}
		})
	}
}

func TestReconcilePresent_restartAfterLostCreateAndReadbackFailure(t *testing.T) {
	// Given POST applies but its response and first read-back both fail.
	f := newReconcileFixture(t)
	f.provider.faultMethod, f.provider.faultStatus, f.provider.lost, f.provider.failReadAfterWrite = http.MethodPost, 503, true, true
	// When the first attempt fails transiently and a new reconciler resumes.
	if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	assertReconcileState(t, f, models.ManagedDNSPending, models.ManagedDNSProviderUnavailable, 1)
	client := NewCloudflareDNSClient("secret")
	client.baseURL, client.httpClient = f.server.URL, f.server.Client()
	restarted := NewManagedEntryDNSReconciler(ManagedEntryDNSReconcileDependencies{Store: NewManagedEntryDNSWorkerStore(f.pool), Client: client, Intent: entryConfig, Now: func() time.Time { return reconcileNow }, Jitter: func() float64 { return 0 }})
	if err := restarted.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	// Then discovery adopts the one owned record without another POST.
	assertReconcileState(t, f, models.ManagedDNSReady, "", 1)
}

func TestReconcilePresent_foreignRecordsFailClosed(t *testing.T) {
	for _, typ := range []string{"A", "CNAME", "NS"} {
		t.Run(typ, func(t *testing.T) {
			// Given foreign same-name data even when owned data also exists.
			f := newReconcileFixture(t)
			s := f.state(t)
			f.seed(t, "A", s.marker, s.ipv4)
			f.seed(t, typ, "other", "192.0.2.8")
			// When
			if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
				t.Fatal(err)
			}
			// Then
			assertReconcileState(t, f, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, 2)
		})
	}
}

func TestReconcilePresent_cachedMarkerRemovedFailsClosed(t *testing.T) {
	// Given a cached ID that is no longer owned.
	f := newReconcileFixture(t)
	id := f.seed(t, "A", "other", "192.0.2.8")
	if _, err := f.pool.Exec(t.Context(), `UPDATE managed_entry_dns SET provider_record_id=$2 WHERE owner_node_id=$1`, f.owner, id); err != nil {
		t.Fatal(err)
	}
	// When
	if err := f.reconciler.ReconcileOwner(t.Context(), f.owner); err != nil {
		t.Fatal(err)
	}
	// Then
	assertReconcileState(t, f, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, 1)
}

func TestReconcilePresent_providerErrorsClassified(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			// Given provider discovery failure.
			f := newReconcileFixture(t)
			f.provider.faultMethod, f.provider.faultStatus, f.provider.retryAfter = http.MethodGet, status, "120"
			// When
			err := f.reconciler.ReconcileOwner(t.Context(), f.owner)
			// Then
			if err != nil {
				t.Fatal(err)
			}
			if status == 429 || status == 503 {
				assertReconcileState(t, f, models.ManagedDNSPending, models.ManagedDNSProviderUnavailable, 0)
				var next time.Time
				if err := f.pool.QueryRow(t.Context(), `SELECT next_attempt_at FROM managed_entry_dns WHERE owner_node_id=$1`, f.owner).Scan(&next); err != nil || !next.Equal(reconcileNow.Add(120*time.Second)) {
					t.Fatalf("retry=%v %v", next, err)
				}
			} else {
				assertReconcileState(t, f, models.ManagedDNSError, models.ManagedDNSProviderRejected, 0)
			}
		})
	}
}

func TestReconcilePresent_staleGenerationOnDelayedCreate(t *testing.T) {
	// Given a provider POST blocked while a new intent generation is persisted.
	f := newReconcileFixture(t)
	f.provider.started, f.provider.resume = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- f.reconciler.ReconcileOwner(t.Context(), f.owner) }()
	<-f.provider.started
	if _, err := f.pool.Exec(t.Context(), `UPDATE managed_entry_dns SET generation=generation+1 WHERE owner_node_id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	// When
	close(f.provider.resume)
	// Then
	if err := <-done; !errors.Is(err, ErrManagedEntryDNSStale) {
		t.Fatalf("stale=%v", err)
	}
	if len(f.records()) != 1 || f.state(t).status == "ready" {
		t.Fatalf("state=%+v records=%+v", f.state(t), f.records())
	}
}
