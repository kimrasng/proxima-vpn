package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

func TestPHS028LockRejectsMissingNodeAndWrongTransaction(t *testing.T) {
	// Given an absent ID and two independent transactions.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	tx := phs028ServiceTx(t, pool)
	other := phs028ServiceTx(t, pool)
	// When locking the missing node or using a token with a different transaction.
	_, err := LockRealityNode(t.Context(), tx, "00000000-0000-0000-0000-000000000001")
	phs028Kind(t, err, RealitySNIMissingNode)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	// Then a mismatched transaction is refused without a write.
	phs028Kind(t, UpdateCanonicalRealitySNI(t.Context(), other, locked, "www.cloudflare.com"), RealitySNILockMismatch)
}

func TestPHS028LockPropagatesDatabaseError(t *testing.T) {
	// Given a transaction that has already been rolled back.
	pool := phs028ServiceDB(t)
	tx := phs028ServiceTx(t, pool)
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	// When acquiring a row lock.
	_, err := LockRealityNode(t.Context(), tx, "00000000-0000-0000-0000-000000000001")
	// Then pgx's closed transaction error is preserved rather than turned semantic.
	if !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("error=%v", err)
	}
}

func TestPHS028ConcurrentTransactionsSerializeCanonicalAndProspectiveMutation(t *testing.T) {
	// Given tx A holding a node lock, while tx B attempts the same lock.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	phs028ServiceInbound(t, pool, node, "vless_reality", `{"server_names":["a.example.test","b.example.test"]}`, true)
	a := phs028ServiceTx(t, pool)
	aLocked, err := LockRealityNode(t.Context(), a, node)
	if err != nil {
		t.Fatal(err)
	}
	b := phs028ServiceTx(t, pool)
	var backend int
	if err := b.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&backend); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	type outcome struct {
		locked *LockedRealityNode
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		close(started)
		locked, err := LockRealityNode(t.Context(), b, node)
		done <- outcome{locked, err}
	}()
	<-started
	// When PostgreSQL reports B waiting on the row lock, A commits its canonical choice.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT wait_event_type = 'Lock' FROM pg_stat_activity WHERE pid=$1),false)`, backend).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("second transaction never waited on the node lock")
		}
	}
	if err := UpdateCanonicalRealitySNI(t.Context(), a, aLocked, "a.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := <-done
	if second.err != nil {
		t.Fatal(second.err)
	}
	// Then B sees A's committed canonical and cannot accept an incompatible listener.
	phs028Kind(t, ValidateProspectiveRealityListeners(t.Context(), b, second.locked, ProspectiveRealityListeners{Listeners: []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"b.example.test"}}}}), RealitySNIMismatch)
	if err := b.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni FROM nodes WHERE id=$1`, node).Scan(&name); err != nil || name != "a.example.test" {
		t.Fatalf("canonical=%q err=%v", name, err)
	}
}

func TestPHS028ConcurrentTransactionsAcceptCompatibleProspectiveAfterCommit(t *testing.T) {
	// Given tx A holding the node lock while tx B attempts to acquire it.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	phs028ServiceInbound(t, pool, node, "vless_reality", `{"server_names":["a.example.test","b.example.test"]}`, true)
	a := phs028ServiceTx(t, pool)
	aLocked, err := LockRealityNode(t.Context(), a, node)
	if err != nil {
		t.Fatal(err)
	}
	b := phs028ServiceTx(t, pool)
	var backend int
	if err := b.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&backend); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		locked *LockedRealityNode
		err    error
	}
	done := make(chan outcome, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		locked, err := LockRealityNode(t.Context(), b, node)
		done <- outcome{locked, err}
	}()
	<-started
	// When B is observed waiting, A selects a canonical name and commits.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT wait_event_type='Lock' FROM pg_stat_activity WHERE pid=$1),false)`, backend).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("second transaction never waited on node lock")
		}
	}
	if err := UpdateCanonicalRealitySNI(t.Context(), a, aLocked, "a.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := <-done
	if second.err != nil {
		t.Fatal(second.err)
	}
	compatible := ProspectiveRealityListeners{Listeners: []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"a.example.test"}}}}
	if err := ValidateProspectiveRealityListeners(t.Context(), b, second.locked, compatible); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then B retains A's canonical name, with a single valid state.
	var name, source, status string
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni,reality_sni_source,reality_sni_status FROM nodes WHERE id=$1`, node).Scan(&name, &source, &status); err != nil {
		t.Fatal(err)
	}
	if name != "a.example.test" || source != "admin" || status != "valid" {
		t.Fatalf("state=%q %q %q", name, source, status)
	}
}
