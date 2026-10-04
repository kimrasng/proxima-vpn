package services

import "testing"

func TestPHS028NotApplicablePreservesCanonicalAndSource(t *testing.T) {
	for _, tc := range []struct {
		name, canonical, source string
	}{
		{"unset", "", ""},
		{"retained", "keep.example.test", "admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a locked node with either unset SNI or a retained stale conflict.
			pool := phs028ServiceDB(t)
			node := phs028ServiceNode(t, pool)
			if tc.canonical != "" {
				if _, err := pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni=$2, reality_sni_source=$3, reality_sni_status='conflict', reality_sni_error_code='listener_mismatch' WHERE id=$1`, node, tc.canonical, tc.source); err != nil {
					t.Fatal(err)
				}
			}
			tx := phs028ServiceTx(t, pool)
			locked, err := LockRealityNode(t.Context(), tx, node)
			if err != nil {
				t.Fatal(err)
			}

			// When the caller marks the empty effective set not applicable.
			if err := MarkRealitySNINotApplicable(t.Context(), tx, locked); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}

			// Then only status and error change, preserving nullable canonical/source.
			var canonical, source, status, code *string
			if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, node).Scan(&canonical, &source, &status, &code); err != nil {
				t.Fatal(err)
			}
			if status == nil || *status != "not_applicable" || code != nil {
				t.Fatalf("status=%v error=%v", status, code)
			}
			if tc.canonical == "" && canonical != nil || tc.canonical != "" && (canonical == nil || *canonical != tc.canonical) || tc.source == "" && source != nil || tc.source != "" && (source == nil || *source != tc.source) {
				t.Fatalf("canonical=%v source=%v", canonical, source)
			}
		})
	}
}

func TestPHS028NotApplicableRejectsWrongTransaction(t *testing.T) {
	// Given a node locked by one transaction.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	tx := phs028ServiceTx(t, pool)
	other := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}

	// When another transaction attempts to persist not-applicable state.
	err = MarkRealitySNINotApplicable(t.Context(), other, locked)

	// Then the lock token is rejected before a write.
	phs028Kind(t, err, RealitySNILockMismatch)
}
