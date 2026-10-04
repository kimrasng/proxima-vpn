package services

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
)

func phs028ServiceDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL required for PHS028 DB tests")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func phs028ServiceNode(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `INSERT INTO nodes (name, api_key, ip) VALUES ($1, 'test', '203.0.113.10') RETURNING id::text`, t.Name()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM nodes WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	})
	return id
}

func phs028ServiceInbound(t *testing.T, pool *pgxpool.Pool, node, protocol, settings string, enabled bool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO inbounds (node_id, protocol, port, tag, settings, enabled) VALUES ($1, $2, 21001, 'primary', $3::jsonb, $4)`, node, protocol, settings, enabled)
	if err != nil {
		t.Fatal(err)
	}
}

func phs028ServiceTx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func phs028Kind(t *testing.T, err error, kind RealitySNIErrorKind) {
	t.Helper()
	var semantic *RealitySNIError
	if !errors.As(err, &semantic) || semantic.Kind != kind {
		t.Fatalf("error=%v want kind=%s", err, kind)
	}
}

func TestPHS028CanonicalUpdateNormalizesAndPersistsAdminState(t *testing.T) {
	// Given an enabled listener and a locked node in a transaction.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	phs028ServiceInbound(t, pool, node, "vless_reality", `{"server_names":["SNI.Example.Test"]}`, true)
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	// When the admin selects a permitted root-dotted hostname.
	err = UpdateCanonicalRealitySNI(t.Context(), tx, locked, "SNI.Example.Test.")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then the stored canonical state is normalized, admin-sourced and valid.
	var name, source, status string
	var reason *string
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, node).Scan(&name, &source, &status, &reason); err != nil {
		t.Fatal(err)
	}
	if name != "sni.example.test" || source != "admin" || status != "valid" || reason != nil {
		t.Fatalf("state=%q %q %q %v", name, source, status, reason)
	}
}

func TestPHS028CanonicalUpdateRejectsMalformedMissingAndMismatchWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, settings, candidate string
		kind                                RealitySNIErrorKind
	}{
		{"malformed", "vless_reality", `{"server_names":["one.example.test"]}`, "bad name", RealitySNIMalformed},
		{"no listener", "vmess_ws", `{}`, "one.example.test", RealitySNINoListener},
		{"mismatch", "vless_reality", `{"server_names":["one.example.test"]}`, "other.example.test", RealitySNIMismatch},
		{"invalid listener", "vless_reality", `{"server_names":["bad name"]}`, "one.example.test", RealitySNIInvalidListener},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a previously stored canonical value.
			pool := phs028ServiceDB(t)
			node := phs028ServiceNode(t, pool)
			phs028ServiceInbound(t, pool, node, tc.protocol, tc.settings, true)
			if _, err := pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni='old.example.test', reality_sni_source='admin', reality_sni_status='valid' WHERE id=$1`, node); err != nil {
				t.Fatal(err)
			}
			tx := phs028ServiceTx(t, pool)
			locked, err := LockRealityNode(t.Context(), tx, node)
			if err != nil {
				t.Fatal(err)
			}
			// When validation fails and the caller rolls back.
			phs028Kind(t, UpdateCanonicalRealitySNI(t.Context(), tx, locked, tc.candidate), tc.kind)
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Then the old value is preserved.
			var name string
			if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni FROM nodes WHERE id=$1`, node).Scan(&name); err != nil || name != "old.example.test" {
				t.Fatalf("name=%q err=%v", name, err)
			}
		})
	}
}

func TestPHS028EntryLookupByLiveNodeNeverUsesDetachedOwnerOrIP(t *testing.T) {
	// Given one live managed hostname and another detached owner record.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	other := phs028ServiceNode(t, pool)
	if _, err := pool.Exec(t.Context(), `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname) VALUES ($1,$1,'entry.example.test'), ($2,NULL,'detached.example.test')`, node, other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id IN ($1,$2)`, node, other)
		if err != nil {
			t.Error(err)
		}
	})
	// When reading by the live node and the detached owner's node ID.
	got, err := ManagedEntryHostname(t.Context(), pool, node)
	if err != nil {
		t.Fatal(err)
	}
	_, missing := ManagedEntryHostname(t.Context(), pool, other)
	// Then the only available value is the stored live hostname.
	if got != "entry.example.test" || !errors.Is(missing, ErrManagedEntryUnavailable) {
		t.Fatalf("hostname=%q detached=%v", got, missing)
	}
}

func TestPHS028CanonicalUpdatePropagatesDatabaseFailureAfterLock(t *testing.T) {
	// Given a locked node whose transaction has been aborted by a SQL failure.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), `SELECT 1/0`)
	if err == nil {
		t.Fatal("expected database error")
	}
	// When validating an otherwise valid candidate.
	err = UpdateCanonicalRealitySNI(t.Context(), tx, locked, "www.cloudflare.com")
	// Then the DB failure is not collapsed into a semantic error.
	var semantic *RealitySNIError
	if err == nil || errors.As(err, &semantic) {
		t.Fatalf("error=%v", err)
	}
}

func TestPHS028CanonicalUpdateIsUndoneWithCallerTransactionRollback(t *testing.T) {
	// Given a previous canonical and an enabled listener accepting its replacement.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	phs028ServiceInbound(t, pool, node, "vless_reality", `{"server_names":["new.example.test"]}`, true)
	if _, err := pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni='old.example.test',reality_sni_source='admin',reality_sni_status='conflict',reality_sni_error_code='listener_mismatch' WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	// When an update succeeds but the caller rolls back its transaction.
	if err := UpdateCanonicalRealitySNI(t.Context(), tx, locked, "new.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then the previous canonical and its conflict status remain unchanged.
	var name, status string
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni,reality_sni_status FROM nodes WHERE id=$1`, node).Scan(&name, &status); err != nil {
		t.Fatal(err)
	}
	if name != "old.example.test" || status != "conflict" {
		t.Fatalf("state=%q %q", name, status)
	}
}
