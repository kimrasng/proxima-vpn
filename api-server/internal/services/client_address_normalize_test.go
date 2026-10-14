package services

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func TestNormalizeClientAddress(t *testing.T) {
	valid := []struct{ in, want string }{
		{"203.0.113.7", "203.0.113.7"},
		{"::ffff:203.0.113.7", "203.0.113.7"},
		{"2001:db8:1:2:aaaa:bbbb:cccc:dddd", "2001:db8:1:2::"},
		{"2001:db8:1:2:1111:2222:3333:4444", "2001:db8:1:2::"},
		{"2001:DB8:1:2::1", "2001:db8:1:2::"},
		{"2001:db8:1:3::1", "2001:db8:1:3::"},
		{"2001:db8:1:2::", "2001:db8:1:2::"},
	}
	for _, tc := range valid {
		got, err := NormalizeClientAddress(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("NormalizeClientAddress(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
		// Idempotent: a stored key may be fed back through unchanged.
		again, err := NormalizeClientAddress(got)
		if err != nil || again != got {
			t.Errorf("not idempotent for %q: %q -> %q (%v)", tc.in, got, again, err)
		}
	}
	invalid := []string{
		"", "hostname", "203.0.113.7:443", "0.0.0.0", "::", "224.0.0.1", "ff02::1",
		"fe80::1%eth0", "::1", "::2", "0:0:0:0:ffff::1",
	}
	for _, in := range invalid {
		if got, err := NormalizeClientAddress(in); err == nil {
			t.Errorf("NormalizeClientAddress(%q) = %q, want error", in, got)
		} else if strings.Contains(err.Error(), in) && in != "" {
			t.Errorf("error leaks the address: %v", err)
		}
	}
}

func TestIPv6PrivacyRotationIsOneClientAddress(t *testing.T) {
	const exit = "10.0.2.2"
	conn := func(peer string, port int, accepted int64) exitConnection {
		return exitConnection{exitID: closureExitA, connection: nodeprov.TunnelConnection{
			UUID: closureCred, PeerIP: peer, PeerPort: port, LocalIP: exit, LocalPort: 8443, AcceptedAt: accepted, IdleMillis: 5}}
	}
	got, err := joinClientAddresses([]exitConnection{
		conn("2001:db8:1:2:aaaa::1", 40001, 100),
		conn("2001:db8:1:2:bbbb::2", 40002, 200),
		conn("2001:db8:1:9::1", 40003, 300),
	}, map[string]bool{}, nil)
	if err != nil || !got.Complete {
		t.Fatalf("join: %+v %v", got, err)
	}
	if want := []string{"2001:db8:1:2::", "2001:db8:1:9::"}; !reflect.DeepEqual(got.Addresses, want) {
		t.Fatalf("addresses = %v, want %v", got.Addresses, want)
	}
	if got.Starts["2001:db8:1:2::"] != 100 {
		t.Fatalf("rotated address reset its age: %v", got.Starts)
	}
	if n := len(got.Transports["2001:db8:1:2::"]); n != 2 {
		t.Fatalf("rotated /64 should own both transports for closure, got %d", n)
	}
	targets, err := ClientAddressClosureTargets(got, []string{"2001:db8:1:2::"})
	if err != nil || len(targets) != 2 {
		t.Fatalf("closing a /64 must close every transport of it: %d %v", len(targets), err)
	}
}

func TestObservationIdleIsFreshestTransportPerAddress(t *testing.T) {
	mk := func(port int, idle int64) exitConnection {
		c := closureConn("198.51.100.7", port, int64(port))
		c.IdleMillis = idle
		return exitConnection{exitID: closureExitA, connection: c}
	}
	got, err := joinClientAddresses([]exitConnection{mk(1, 500_000), mk(2, 40), mk(3, 90_000)}, map[string]bool{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Idle["198.51.100.7"] != 40 {
		t.Fatalf("idle = %v, an address is only as stale as its freshest transport", got.Idle)
	}
	if addressIsStale(got, "198.51.100.7") {
		t.Fatal("address with a live transport reported stale")
	}
	if addressIsStale(got, "203.0.113.1") {
		t.Fatal("address without idle evidence must never be stale")
	}
}

func staleObservation(starts, idle map[string]int64) ClientAddressObservation {
	addresses := make([]string, 0, len(starts))
	for a := range starts {
		addresses = append(addresses, a)
	}
	return ClientAddressObservation{Complete: true, Addresses: addresses, Starts: starts, Idle: idle}
}

func TestPolicyDeniesAbandonedAddressBeforeNewerActiveOne(t *testing.T) {
	// Phone switched Wi-Fi -> LTE: the old address is older but dead, the new
	// one is newer and live. Plain "newest denied" would cut the live client.
	const wifi, lte, other = "198.51.100.1", "198.51.100.2", "198.51.100.3"
	obs := staleObservation(
		map[string]int64{wifi: 100, other: 150, lte: 200},
		map[string]int64{wifi: 600_000, other: 10, lte: 20},
	)
	got, err := ProposeClientAddressPolicy(obs, 2)
	if err != nil || !got.Known || !reflect.DeepEqual(got.Denied, []string{wifi}) || !reflect.DeepEqual(got.Allowed, []string{lte, other}) { // Allowed is sorted
		t.Fatalf("policy = %+v, %v", got, err)
	}
	if d := DecideClientAddressLimit(obs, 2); d.Target != wifi || !d.OverLimit {
		t.Fatalf("admin candidate %+v disagrees with the enforcement policy", d)
	}
}

func TestPolicyWithoutIdleEvidenceKeepsNewestDenied(t *testing.T) {
	obs := staleObservation(map[string]int64{"198.51.100.1": 100, "198.51.100.2": 200, "198.51.100.3": 300}, nil)
	got, err := ProposeClientAddressPolicy(obs, 2)
	if err != nil || !reflect.DeepEqual(got.Denied, []string{"198.51.100.3"}) {
		t.Fatalf("policy = %+v, %v", got, err)
	}
	if d := DecideClientAddressLimit(obs, 2); d.Target != "198.51.100.3" {
		t.Fatalf("candidate = %+v", d)
	}
}

func TestPolicyStaleOnlyRanksAmongSameGroup(t *testing.T) {
	// Two abandoned addresses, cap 1: the older abandoned one is kept (it sorts
	// first), the newer abandoned one is denied. An active address always wins.
	const live, oldDead, newDead = "198.51.100.1", "198.51.100.2", "198.51.100.3"
	obs := staleObservation(
		map[string]int64{live: 300, oldDead: 100, newDead: 200},
		map[string]int64{live: 5, oldDead: 300_000, newDead: 300_000},
	)
	got, err := ProposeClientAddressPolicy(obs, 1)
	if err != nil || !reflect.DeepEqual(got.Allowed, []string{live}) || !reflect.DeepEqual(got.Denied, []string{oldDead, newDead}) {
		t.Fatalf("policy = %+v, %v", got, err)
	}
}

func TestPolicyTieStillAmbiguousWithinOneGroupButNotAcrossGroups(t *testing.T) {
	const a, b = "198.51.100.1", "198.51.100.2"
	tied := staleObservation(map[string]int64{a: 100, b: 100}, map[string]int64{a: 5, b: 5})
	if got, err := ProposeClientAddressPolicy(tied, 1); err != nil || got.Known || got.Reason != "ambiguous_cutoff" {
		t.Fatalf("same-group tie must stay ambiguous: %+v %v", got, err)
	}
	if d := DecideClientAddressLimit(tied, 1); d.Target != "" || d.Reason != "ambiguous_newest" {
		t.Fatalf("admin tie must stay ambiguous: %+v", d)
	}
	split := staleObservation(map[string]int64{a: 100, b: 100}, map[string]int64{a: 5, b: 400_000})
	got, err := ProposeClientAddressPolicy(split, 1)
	if err != nil || !got.Known || !reflect.DeepEqual(got.Denied, []string{b}) {
		t.Fatalf("a live/abandoned split is decidable even with equal starts: %+v %v", got, err)
	}
}

func TestStaleThresholdBoundary(t *testing.T) {
	obs := staleObservation(map[string]int64{"198.51.100.1": 1}, map[string]int64{
		"198.51.100.1": staleAddressIdleMillis - 1, "198.51.100.2": staleAddressIdleMillis})
	if addressIsStale(obs, "198.51.100.1") || !addressIsStale(obs, "198.51.100.2") {
		t.Fatalf("threshold %d: just-below must be live, at-threshold stale", staleAddressIdleMillis)
	}
}

// Redis-backed: admission and release must key IPv6 identically, otherwise the
// release sweep would never match the slot admission created.
func TestIPv6SlotsShareOnePerSlash64AcrossAdmissionAndRelease(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	account := uuid.NewString()
	key := AddressSlotKey(account)
	t.Cleanup(func() { _ = rdb.Del(ctx, key, key+":sequence", key+":absence", key+":epoch").Err() })

	for _, source := range []string{"2001:db8:1:2:aaaa::1", "2001:db8:1:2:bbbb::2", "2001:db8:1:2:cccc::3"} {
		ok, err := ReserveObservedClientAddress(ctx, rdb, account, source, 1)
		if err != nil || !ok {
			t.Fatalf("privacy-rotated address %q denied under cap 1: ok=%v err=%v", source, ok, err)
		}
	}
	if n, _ := rdb.SCard(ctx, key).Result(); n != 1 {
		t.Fatalf("slots = %d, want 1 for one /64", n)
	}
	if ok, err := ReserveObservedClientAddress(ctx, rdb, account, "2001:db8:1:9::1", 1); err != nil || ok {
		t.Fatalf("a different /64 must be a second client: ok=%v err=%v", ok, err)
	}
	if members, _ := rdb.SMembers(ctx, key).Result(); !reflect.DeepEqual(members, []string{"2001:db8:1:2::"}) {
		t.Fatalf("slot key = %v, want the /64 network address", members)
	}

	// A live observation of the same /64 under a different interface id keeps
	// the slot: release must normalize identically to admission.
	fence, err := CaptureAddressReservationFence(ctx, rdb, account)
	if err != nil {
		t.Fatal(err)
	}
	live := ClientAddressObservation{Complete: true, Addresses: []string{"2001:db8:1:2:dead:beef::1"}, SnapshotID: "s1"}
	if released, err := ReconcileAbsentClientAddresses(ctx, rdb, account, live, time.Now().UnixMilli()-10, fence); err != nil || len(released) != 0 {
		t.Fatalf("live /64 treated as absent: %v %v", released, err)
	}
	if h, _ := rdb.HExists(ctx, key+":absence", "2001:db8:1:2::").Result(); h {
		t.Fatal("absence evidence recorded for a /64 that is live")
	}
}
