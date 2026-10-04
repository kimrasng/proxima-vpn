package services

import (
	"context"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

var entryConfig = ManagedEntryDNSIntentConfig{Enabled: true, ZoneID: "0123456789abcdef0123456789abcdef", BaseDomain: "Entry.Example.Test."}

type entryState struct {
	id, hostname, marker, zone, action, status, ipv4 string
	errorCode, recordID                              *string
	generation                                       int64
	queued                                           bool
}

func entryNode(t *testing.T, pool *pgxpool.Pool, role string) string {
	t.Helper()
	id := phs028ServiceNode(t, pool)
	if _, err := pool.Exec(t.Context(), `UPDATE nodes SET role=$2 WHERE id=$1`, id, role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, id); err != nil {
			t.Error(err)
		}
	})
	return id
}

func ensureEntry(t *testing.T, pool *pgxpool.Pool, node string, cfg ManagedEntryDNSIntentConfig) {
	t.Helper()
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureManagedEntryDNSIntent(t.Context(), tx, locked, cfg); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func readEntry(t *testing.T, pool *pgxpool.Pool, node string) entryState {
	t.Helper()
	var s entryState
	err := pool.QueryRow(t.Context(), `SELECT id::text, COALESCE(hostname,''), COALESCE(ownership_marker,''), COALESCE(cloudflare_zone_id,''), desired_action, COALESCE(dns_status,''), COALESCE(host(desired_ipv4),''), error_code, provider_record_id, generation, next_attempt_at IS NOT NULL FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&s.id, &s.hostname, &s.marker, &s.zone, &s.action, &s.status, &s.ipv4, &s.errorCode, &s.recordID, &s.generation, &s.queued)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestManagedEntryIntentAllocatesEntropyForForwardingRole(t *testing.T) {
	// Given a forwarding node and an enabled zone.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "both")
	// When registration ensures DNS intent.
	ensureEntry(t, pool, node, entryConfig)
	// Then the random label is 128 bits of lowercase hex and ownership is bound to row ID.
	s := readEntry(t, pool, node)
	if !regexp.MustCompile(`^[0-9a-f]{32}\.entry\.example\.test$`).MatchString(s.hostname) || s.marker != "proxima-entry:"+s.id || s.zone != entryConfig.ZoneID || s.ipv4 != "203.0.113.10" || s.action != "present" || !s.queued {
		t.Fatalf("intent=%+v", s)
	}
	other := entryNode(t, pool, "relay")
	ensureEntry(t, pool, other, entryConfig)
	if readEntry(t, pool, other).hostname == s.hostname {
		t.Fatal("independent allocations collided")
	}
}

func TestManagedEntryIntentPreservesReadyHostnameAndUpdatesIPv4Generation(t *testing.T) {
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	first := readEntry(t, pool, node)
	if _, err := pool.Exec(t.Context(), `UPDATE managed_entry_dns SET dns_status='ready', next_attempt_at=NULL, provider_record_id='record-1' WHERE owner_node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	// Given a ready registration, when the same address is registered again, it remains ready.
	ensureEntry(t, pool, node, entryConfig)
	ready := readEntry(t, pool, node)
	if ready.hostname != first.hostname || ready.marker != first.marker || ready.zone != first.zone || ready.status != "ready" || ready.generation != first.generation || ready.queued {
		t.Fatalf("ready=%+v", ready)
	}
	// When the locked node's address changes, only desired address and generation change.
	if _, err := pool.Exec(t.Context(), `UPDATE nodes SET ip='203.0.113.11' WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	ensureEntry(t, pool, node, entryConfig)
	changed := readEntry(t, pool, node)
	if changed.ipv4 != "203.0.113.11" || changed.generation != ready.generation+1 || !changed.queued || changed.hostname != ready.hostname || changed.recordID == nil || *changed.recordID != "record-1" {
		t.Fatalf("changed=%+v", changed)
	}
}

func TestManagedEntryIntentInvalidConfigAndFrozenBinding(t *testing.T) {
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	// Given an absent config, when ensuring, registration succeeds with visible invalid state.
	ensureEntry(t, pool, node, ManagedEntryDNSIntentConfig{})
	missing := readEntry(t, pool, node)
	if missing.hostname != "" || missing.status != "error" || missing.errorCode == nil || *missing.errorCode != "invalid_configuration" {
		t.Fatalf("missing=%+v", missing)
	}
	// When config becomes valid, allocate once; subsequent zone/base drift cannot rotate it.
	ensureEntry(t, pool, node, entryConfig)
	bound := readEntry(t, pool, node)
	if bound.generation != missing.generation+1 {
		t.Fatalf("configuration recovery generation=%d want %d", bound.generation, missing.generation+1)
	}
	for _, cfg := range []ManagedEntryDNSIntentConfig{
		{Enabled: true, ZoneID: "abcdef0123456789abcdef0123456789", BaseDomain: "entry.example.test"},
		{Enabled: true, ZoneID: entryConfig.ZoneID, BaseDomain: "other.example.test"},
	} {
		ensureEntry(t, pool, node, cfg)
		s := readEntry(t, pool, node)
		if s.hostname != bound.hostname || s.zone != bound.zone || s.marker != bound.marker || s.status != "error" || s.errorCode == nil || *s.errorCode != "invalid_configuration" {
			t.Fatalf("frozen=%+v", s)
		}
	}
}

func TestManagedEntryIntentDisabledConfigRetainsIdentity(t *testing.T) {
	// Given an allocated hostname, when DNS is disabled, identity is not rotated or erased.
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "relay")
	ensureEntry(t, pool, node, entryConfig)
	before := readEntry(t, pool, node)
	ensureEntry(t, pool, node, ManagedEntryDNSIntentConfig{})
	after := readEntry(t, pool, node)
	if after.hostname != before.hostname || after.zone != before.zone || after.marker != before.marker || after.status != "error" || after.errorCode == nil || *after.errorCode != "invalid_configuration" {
		t.Fatalf("disabled=%+v", after)
	}
}

func TestManagedEntryIntentRejectsLockMismatchAndSkipsExit(t *testing.T) {
	pool := phs028ServiceDB(t)
	node := entryNode(t, pool, "exit")
	ensureEntry(t, pool, node, entryConfig)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM managed_entry_dns WHERE owner_node_id=$1`, node).Scan(&count); err != nil || count != 0 {
		t.Fatalf("exit rows=%d err=%v", count, err)
	}
	tx, other := phs028ServiceTx(t, pool), phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	phs028Kind(t, EnsureManagedEntryDNSIntent(t.Context(), other, locked, entryConfig), RealitySNILockMismatch)
}
