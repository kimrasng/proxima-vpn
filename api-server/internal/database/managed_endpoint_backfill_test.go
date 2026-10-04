package database

// allow: SIZE_OK - WP-3 confines real-Postgres scenarios to its sole owned test file.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPHS028SNIConstraintRetainsCanonicalOnVisibleConflict(t *testing.T) {
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni = 'Original.Example.Test', reality_sni_source = 'admin', reality_sni_status = 'valid' WHERE id = $1`, node); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `UPDATE nodes SET reality_sni_status = 'conflict', reality_sni_error_code = 'listener_mismatch' WHERE id = $1`, node)
	if err != nil {
		t.Fatalf("canonical conflict should be visible, not forbidden: %v", err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "Original.Example.Test", "admin", "conflict", "listener_mismatch")
}

type phs028SNIState struct {
	name, source, status, reason *string
}

func phs028State(t *testing.T, pool *pgxpool.Pool, node string) phs028SNIState {
	t.Helper()
	var state phs028SNIState
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id = $1`, node).Scan(&state.name, &state.source, &state.status, &state.reason); err != nil {
		t.Fatalf("read SNI state: %v", err)
	}
	return state
}

func phs028ExpectSNI(t *testing.T, state phs028SNIState, name, source, status, reason string) {
	t.Helper()
	for field, value := range map[string]struct {
		got  *string
		want string
	}{
		"name": {state.name, name}, "source": {state.source, source},
		"status": {state.status, status}, "reason": {state.reason, reason},
	} {
		if (value.got == nil && value.want != "") || (value.got != nil && *value.got != value.want) {
			t.Errorf("%s = %v, want %q", field, value.got, value.want)
		}
	}
}

func phs028Inbound(t *testing.T, pool *pgxpool.Pool, node, protocol, names string, enabled bool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO inbounds (node_id, protocol, port, tag, settings, enabled) VALUES ($1, $2, COALESCE((SELECT MAX(port) + 1 FROM inbounds WHERE node_id = $1), 22001), gen_random_uuid()::text, $3::jsonb, $4)`, node, protocol, names, enabled)
	if err != nil {
		t.Fatalf("insert inbound: %v", err)
	}
}

func phs028AllowMultipleInbounds(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `DROP INDEX IF EXISTS idx_inbounds_one_protocol_per_node`); err != nil {
		t.Fatal(err)
	}
}

func TestPHS028BackfillChoosesCommonMemberNotFirstAndPreservesSettings(t *testing.T) {
	pool, ctx := phs028DB(t)
	t.Cleanup(func() {
		if err := Migrate(context.Background(), pool); err != nil {
			t.Errorf("restore inbound index: %v", err)
		}
	})
	node := phs028Node(t, pool, t.Name())
	phs028AllowMultipleInbounds(t, pool)
	phs028Inbound(t, pool, node, "vless_reality", `{"server_names":["z.example.test","COMMON.Example.test","common.example.test"]}`, true)
	phs028Inbound(t, pool, node, "vless_reality", `{"server_names":["other.example.test","common.example.test."]}`, true)
	var before, after string
	if err := pool.QueryRow(ctx, `SELECT string_agg(settings::text, '|' ORDER BY id) FROM inbounds WHERE node_id = $1`, node).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("backfill migration: %v", err)
		}
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "common.example.test", "backfill", "valid", "")
	if err := pool.QueryRow(ctx, `SELECT string_agg(settings::text, '|' ORDER BY id) FROM inbounds WHERE node_id = $1`, node).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("listener settings changed: before %q after %q", before, after)
	}
}

func TestPHS028BackfillRecordsInvalidAndDisjointListenersWithoutFallback(t *testing.T) {
	pool, ctx := phs028DB(t)
	t.Cleanup(func() {
		if err := Migrate(context.Background(), pool); err != nil {
			t.Errorf("restore inbound index: %v", err)
		}
	})
	phs028AllowMultipleInbounds(t, pool)
	invalid := phs028Node(t, pool, t.Name()+"-invalid")
	phs028Inbound(t, pool, invalid, "vless_reality", `{"server_names":["bad name"]}`, true)
	disjoint := phs028Node(t, pool, t.Name()+"-disjoint")
	phs028Inbound(t, pool, disjoint, "vless_reality", `{"server_names":["one.example.test"]}`, true)
	phs028Inbound(t, pool, disjoint, "vless_reality", `{"server_names":["two.example.test"]}`, true)
	retained := phs028Node(t, pool, t.Name()+"-retained")
	phs028Inbound(t, pool, retained, "vless_reality", `{"server_names":["one.example.test"]}`, true)
	phs028Inbound(t, pool, retained, "vless_reality", `{"server_names":["two.example.test"]}`, true)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni = 'Original.Example.Test', reality_sni_source = 'admin', reality_sni_status = 'valid' WHERE id = $1`, retained); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("semantic conflicts must not stop startup: %v", err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, invalid), "", "backfill", "conflict", "invalid_sni")
	phs028ExpectSNI(t, phs028State(t, pool, disjoint), "", "backfill", "conflict", "no_common_name")
	phs028ExpectSNI(t, phs028State(t, pool, retained), "Original.Example.Test", "admin", "conflict", "no_common_name")
}

func TestPHS028BackfillPreservesAdminCanonicalAcrossCompatibilityChanges(t *testing.T) {
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	phs028Inbound(t, pool, node, "vless_reality", `{"server_names":["same.example.test"]}`, true)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni = 'SAME.Example.Test.', reality_sni_source = 'admin', reality_sni_status = 'valid' WHERE id = $1`, node); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "SAME.Example.Test.", "admin", "valid", "")
	if _, err := pool.Exec(ctx, `UPDATE inbounds SET settings = '{"server_names":["other.example.test"]}' WHERE node_id = $1`, node); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("mismatch must not stop startup: %v", err)
		}
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "SAME.Example.Test.", "admin", "conflict", "listener_mismatch")
}

func TestPHS028BackfillPreservesMalformedCanonicalWithConflict(t *testing.T) {
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	phs028Inbound(t, pool, node, "vless_reality", `{"server_names":["good.example.test"]}`, true)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni = 'bad name', reality_sni_source = 'admin', reality_sni_status = 'valid' WHERE id = $1`, node); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("malformed canonical must not stop startup: %v", err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "bad name", "admin", "conflict", "invalid_sni")
}

func TestPHS028BackfillRollsBackNodeOnDatabaseFailureAndRetries(t *testing.T) {
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	if _, err := pool.Exec(ctx, `CREATE FUNCTION phs028_fail_sni_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected write failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS phs028_fail_sni_update()`) })
	if _, err := pool.Exec(ctx, `CREATE TRIGGER phs028_fail_sni_update BEFORE UPDATE OF reality_sni_status ON nodes FOR EACH ROW WHEN (OLD.id = '`+node+`'::uuid) EXECUTE FUNCTION phs028_fail_sni_update()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS phs028_fail_sni_update ON nodes`)
	})
	if err := Migrate(ctx, pool); err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("expected retryable failure, got %v", err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "", "", "", "")
	if _, err := pool.Exec(ctx, `DROP TRIGGER phs028_fail_sni_update ON nodes`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("retry migration: %v", err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "www.cloudflare.com", "backfill", "valid", "")
}

func TestPHS028ConcurrentMigrateBackfillAgreesOnCanonical(t *testing.T) {
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	phs028Inbound(t, pool, node, "vless_reality", `{"server_names":["z.example.test","a.example.test"]}`, true)
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- Migrate(ctx, pool) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "a.example.test", "backfill", "valid", "")
}
