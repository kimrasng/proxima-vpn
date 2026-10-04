package database

import (
	"bytes"
	"strings"
	"testing"
)

func TestPHS029ReconciliationColumnsHaveDurableTypesAndDefaults(t *testing.T) {
	// Given a real database migrated through the production entrypoint.
	pool, ctx := phs028DB(t)
	// When the six reconciliation columns are inspected.
	for _, tc := range []struct {
		name, dataType, nullable, defaultValue string
	}{
		{"cloudflare_zone_id", "text", "YES", ""},
		{"ownership_marker", "text", "YES", ""},
		{"provider_record_id", "text", "YES", ""},
		{"generation", "bigint", "NO", "1"},
		{"retry_count", "integer", "NO", "0"},
		{"next_attempt_at", "timestamp with time zone", "YES", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dataType, nullable string
			var defaultValue *string
			err := pool.QueryRow(ctx, `SELECT data_type, is_nullable, column_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'managed_entry_dns' AND column_name = $1`, tc.name).Scan(&dataType, &nullable, &defaultValue)
			// Then each column has the exact persistence contract.
			if err != nil {
				t.Fatalf("column %s: %v", tc.name, err)
			}
			if dataType != tc.dataType || nullable != tc.nullable || (defaultValue != nil && *defaultValue != tc.defaultValue) || (defaultValue == nil && tc.defaultValue != "") {
				t.Fatalf("column %s = (%s, %s, %v), want (%s, %s, %s)", tc.name, dataType, nullable, defaultValue, tc.dataType, tc.nullable, tc.defaultValue)
			}
		})
	}
}

func TestPHS029ReconciliationDefaultsAndChecksWhenInserting(t *testing.T) {
	// Given a migrated real database and a PHS-028-style minimal intent.
	pool, ctx := phs028DB(t)
	var id string
	var generation int64
	var retryCount int32
	var zone, marker, record *string
	var nextAttempt *string
	// When the old insert omits all reconciliation fields.
	err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns (owner_node_id) VALUES (gen_random_uuid()) RETURNING id::text, generation, retry_count, cloudflare_zone_id, ownership_marker, provider_record_id, next_attempt_at::text`).Scan(&id, &generation, &retryCount, &zone, &marker, &record, &nextAttempt)
	if err != nil {
		t.Fatalf("insert legacy intent: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id = $1`, id) })
	// Then counters start valid and optional provider state is absent.
	if generation != 1 || retryCount != 0 || zone != nil || marker != nil || record != nil || nextAttempt != nil {
		t.Fatalf("defaults: generation=%d retry=%d zone=%v marker=%v record=%v next=%v", generation, retryCount, zone, marker, record, nextAttempt)
	}
	for _, tc := range []struct{ name, update string }{
		{"zero generation", `UPDATE managed_entry_dns SET generation = 0 WHERE id = $1`},
		{"negative generation", `UPDATE managed_entry_dns SET generation = -1 WHERE id = $1`},
		{"null generation", `UPDATE managed_entry_dns SET generation = NULL WHERE id = $1`},
		{"negative retry", `UPDATE managed_entry_dns SET retry_count = -1 WHERE id = $1`},
		{"null retry", `UPDATE managed_entry_dns SET retry_count = NULL WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// When an invalid counter is stored, then PostgreSQL rejects it.
			_, err := pool.Exec(ctx, tc.update, id)
			if strings.HasPrefix(tc.name, "null") {
				phs028SQLState(t, err, "23502")
			} else {
				phs028SQLState(t, err, "23514")
			}
		})
	}
}

func TestPHS029DueWorkIndexHasNullablePredicateAndOrdering(t *testing.T) {
	// Given the migrated schema.
	pool, ctx := phs028DB(t)
	// When inspecting PostgreSQL's actual index keys and predicate.
	var first, second, predicate string
	err := pool.QueryRow(ctx, `SELECT pg_get_indexdef(i.indexrelid, 1, true), pg_get_indexdef(i.indexrelid, 2, true), pg_get_expr(i.indpred, i.indrelid)
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE i.indrelid = 'managed_entry_dns'::regclass AND c.relname = 'idx_managed_entry_dns_due_work'`).Scan(&first, &second, &predicate)
	// Then due time precedes id, and only scheduled rows are indexed.
	if err != nil || first != "next_attempt_at" || second != "id" || predicate != "(next_attempt_at IS NOT NULL)" {
		t.Fatalf("due index: first=%q second=%q predicate=%q err=%v", first, second, predicate, err)
	}
}

func TestPHS029RepeatedMigrationPreservesLegacyAndReconciliationState(t *testing.T) {
	// Given a complete PHS-028 row, including an orphan-safe owner and cleanup state.
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns
		(owner_node_id, node_id, hostname, desired_ipv4, observed_ipv4, desired_action, dns_status, observed_at, error_code, cleanup_requested_at)
		VALUES ($1, $1, 'upgrade.example.test', '203.0.113.9', '203.0.113.8', 'delete', 'deleting', '2026-01-01T00:00:00Z', 'provider_unavailable', '2026-01-02T00:00:00Z') RETURNING id::text`, node).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id = $1`, id) })
	var legacyBefore []byte
	err = pool.QueryRow(ctx, `SELECT to_jsonb(m) - ARRAY['cloudflare_zone_id', 'ownership_marker', 'provider_record_id', 'generation', 'retry_count', 'next_attempt_at'] FROM managed_entry_dns m WHERE id = $1`, id).Scan(&legacyBefore)
	if err != nil {
		t.Fatal(err)
	}
	// When the production migration runs on that row.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Then every pre-existing value remains byte-for-byte identical.
	var legacyAfter []byte
	err = pool.QueryRow(ctx, `SELECT to_jsonb(m) - ARRAY['cloudflare_zone_id', 'ownership_marker', 'provider_record_id', 'generation', 'retry_count', 'next_attempt_at'] FROM managed_entry_dns m WHERE id = $1`, id).Scan(&legacyAfter)
	if err != nil || !bytes.Equal(legacyBefore, legacyAfter) {
		t.Fatalf("legacy row changed: before=%s after=%s err=%v", legacyBefore, legacyAfter, err)
	}
	// Given populated reconciliation state, when migration repeats twice.
	_, err = pool.Exec(ctx, `UPDATE managed_entry_dns SET cloudflare_zone_id='zone-1', ownership_marker='owner-1', provider_record_id='record-1', generation=7, retry_count=3, next_attempt_at='2026-02-01T00:00:00Z' WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(m) FROM managed_entry_dns m WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	// Then neither old nor new state is rewritten.
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(m) FROM managed_entry_dns m WHERE id=$1`, id).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repeat migration changed row: before=%s after=%s err=%v", before, after, err)
	}
}
