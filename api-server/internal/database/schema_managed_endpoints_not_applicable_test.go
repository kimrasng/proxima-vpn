package database

import "testing"

func TestPHS028NotApplicableConstraintAcceptsConsistentPairsAcrossMigrations(t *testing.T) {
	for _, tc := range []struct {
		name, canonical, source string
	}{
		{"unset", "", ""},
		{"retained admin", "keep.example.test", "admin"},
		{"retained backfill", "backfill.example.test", "backfill"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a newly registered node in real Postgres.
			pool, ctx := phs028DB(t)
			node := phs028Node(t, pool, t.Name())

			// When its SNI becomes not applicable and the constraint migration is repeated.
			if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni=NULLIF($2,''), reality_sni_source=NULLIF($3,''), reality_sni_status='not_applicable', reality_sni_error_code=NULL WHERE id=$1`, node, tc.canonical, tc.source); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := pool.Exec(ctx, managedEndpointMigrations[4]); err != nil {
					t.Fatal(err)
				}
			}

			// Then the consistent pair, status and empty error remain unchanged.
			var canonical, source, status, code *string
			if err := pool.QueryRow(ctx, `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, node).Scan(&canonical, &source, &status, &code); err != nil {
				t.Fatal(err)
			}
			if status == nil || *status != "not_applicable" || code != nil || tc.canonical == "" && canonical != nil || tc.canonical != "" && (canonical == nil || *canonical != tc.canonical) || tc.source == "" && source != nil || tc.source != "" && (source == nil || *source != tc.source) {
				t.Fatalf("state=%v %v %v %v", canonical, source, status, code)
			}
		})
	}
}

func TestPHS028NotApplicableConstraintRejectsHalfPairsEmptyNamesAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, canonical, source, code string
	}{
		{"name without source", "keep.example.test", "", ""},
		{"source without name", "", "admin", ""},
		{"empty name", " ", "admin", ""},
		{"whitespace name", "  ", "backfill", ""},
		{"error with unset pair", "", "", "not_reality"},
		{"error with retained pair", "keep.example.test", "admin", "listener_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a fresh node and an invalid not-applicable SNI combination.
			pool, ctx := phs028DB(t)
			node := phs028Node(t, pool, t.Name())

			// When persisting the invalid combination.
			_, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni=NULLIF($2,''), reality_sni_source=NULLIF($3,''), reality_sni_status='not_applicable', reality_sni_error_code=NULLIF($4,'') WHERE id=$1`, node, tc.canonical, tc.source, tc.code)

			// Then Postgres rejects the row with its consistency constraint.
			phs028SQLState(t, err, "23514")
		})
	}
}
