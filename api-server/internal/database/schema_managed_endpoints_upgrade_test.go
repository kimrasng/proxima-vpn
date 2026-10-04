package database

import (
	"bytes"
	"testing"
)

func TestPHS028MigrationUpgradesPreviousNotApplicableHalfPair(t *testing.T) {
	// Given rows accepted by the previous constraint, including its null-canonical backfill half-pair.
	pool, ctx := phs028DB(t)
	_, err := pool.Exec(ctx, `ALTER TABLE nodes DROP CONSTRAINT nodes_reality_sni_consistency_check;
		ALTER TABLE nodes ADD CONSTRAINT nodes_reality_sni_consistency_check CHECK (
			(reality_sni_status IS NULL AND reality_client_sni IS NULL AND reality_sni_source IS NULL AND reality_sni_error_code IS NULL)
			OR (reality_sni_status = 'valid' AND reality_sni_source IS NOT NULL
				AND reality_client_sni IS NOT NULL AND BTRIM(reality_client_sni) <> '' AND reality_sni_error_code IS NULL)
			OR (reality_sni_status = 'conflict' AND reality_sni_source IS NOT NULL
				AND (reality_client_sni IS NULL OR reality_sni_error_code IS NOT NULL))
			OR (reality_sni_status = 'not_applicable' AND reality_sni_source IS NOT NULL)
		)`)
	if err != nil {
		t.Fatal(err)
	}
	legacy := phs028Node(t, pool, t.Name()+"-legacy")
	retained := phs028Node(t, pool, t.Name()+"-retained")
	valid := phs028Node(t, pool, t.Name()+"-valid")
	phs028Inbound(t, pool, legacy, "vmess_ws", `{}`, true)
	phs028Inbound(t, pool, retained, "vmess_ws", `{}`, true)
	phs028Inbound(t, pool, valid, "vless_reality", `{"server_names":["valid.example.test"]}`, true)
	for _, seed := range []struct{ id, canonical, source, status string }{
		{legacy, "", "backfill", "not_applicable"},
		{retained, "Retained.Example.Test", "admin", "not_applicable"},
		{valid, "valid.example.test", "admin", "valid"},
	} {
		if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni=NULLIF($2,''), reality_sni_source=$3, reality_sni_status=$4, reality_sni_error_code=NULL WHERE id=$1`, seed.id, seed.canonical, seed.source, seed.status); err != nil {
			t.Fatal(err)
		}
	}
	var retainedBefore, validBefore []byte
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(n) FROM nodes n WHERE id=$1`, retained).Scan(&retainedBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(n) FROM nodes n WHERE id=$1`, valid).Scan(&validBefore); err != nil {
		t.Fatal(err)
	}

	// When production migration replaces the constraint, then runs a second time.
	for range 2 {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}

	// Then only the legacy half-pair is normalized, without changing its status/error or other rows.
	var canonical, source, status, code *string
	if err := pool.QueryRow(ctx, `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, legacy).Scan(&canonical, &source, &status, &code); err != nil {
		t.Fatal(err)
	}
	if canonical != nil || source != nil || status == nil || *status != "not_applicable" || code != nil {
		t.Fatalf("upgraded state = %v %v %v %v", canonical, source, status, code)
	}
	for _, unchanged := range []struct {
		id     string
		before []byte
	}{
		{retained, retainedBefore},
		{valid, validBefore},
	} {
		var after []byte
		if err := pool.QueryRow(ctx, `SELECT to_jsonb(n) FROM nodes n WHERE id=$1`, unchanged.id).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(unchanged.before, after) {
			t.Fatalf("migration rewrote unrelated node %s: before=%s after=%s", unchanged.id, unchanged.before, after)
		}
	}
}
