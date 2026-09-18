package services

import (
	"testing"
)

// The admin views read GetAllOnlineUUIDs, which used to consult only the
// uuid-list key. That list is derived from traffic counters read destructively,
// so it arrived empty and the panel showed nobody online while the per-IP
// report held the real picture.
func TestOnlineUUIDsComeFromThePerIPReport(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	_, uuids := seedUserWithDevices(t, ctx, pool, 1)
	node := seedNode(t, ctx, pool, "uuid-src", false)

	publishOnlineIPs(t, ctx, rdb, node, map[string][]onlineIPFixture{
		uuids[0]: {{IP: "203.0.113.10", LastSeen: 100}},
	})

	byUUID, err := tracker.GetAllOnlineUUIDs(ctx)
	if err != nil {
		t.Fatalf("GetAllOnlineUUIDs: %v", err)
	}
	if got := byUUID[uuids[0]]; got != node {
		t.Fatalf("device maps to node %q, want %q - the per-IP report was ignored", got, node)
	}
}

// An agent too old to report addresses still populates the uuid list, and must
// not vanish from the panel.
func TestLegacyUUIDListStillCounts(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	_, uuids := seedUserWithDevices(t, ctx, pool, 1)
	node := seedNode(t, ctx, pool, "uuid-legacy", false)

	key := "node:" + node + ":online"
	if err := rdb.Set(ctx, key, `["`+uuids[0]+`"]`, 0).Err(); err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })

	byUUID, err := tracker.GetAllOnlineUUIDs(ctx)
	if err != nil {
		t.Fatalf("GetAllOnlineUUIDs: %v", err)
	}
	if byUUID[uuids[0]] != node {
		t.Fatal("a legacy agent's report was dropped")
	}
}

// A device present in both reports must resolve once, not twice.
func TestDeviceInBothReportsResolvesOnce(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	_, uuids := seedUserWithDevices(t, ctx, pool, 1)
	node := seedNode(t, ctx, pool, "uuid-both", false)

	publishOnlineIPs(t, ctx, rdb, node, map[string][]onlineIPFixture{
		uuids[0]: {{IP: "203.0.113.11", LastSeen: 100}},
	})
	key := "node:" + node + ":online"
	if err := rdb.Set(ctx, key, `["`+uuids[0]+`"]`, 0).Err(); err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })

	byUUID, err := tracker.GetAllOnlineUUIDs(ctx)
	if err != nil {
		t.Fatalf("GetAllOnlineUUIDs: %v", err)
	}
	count := 0
	for uuid := range byUUID {
		if uuid == uuids[0] {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("device appears %d times, want 1", count)
	}
}

// An empty address list means the device is not connected, whatever the key's
// presence suggests.
func TestDeviceWithNoAddressesIsNotOnline(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	_, uuids := seedUserWithDevices(t, ctx, pool, 1)
	node := seedNode(t, ctx, pool, "uuid-empty", false)

	publishOnlineIPs(t, ctx, rdb, node, map[string][]onlineIPFixture{
		uuids[0]: {},
	})

	byUUID, err := tracker.GetAllOnlineUUIDs(ctx)
	if err != nil {
		t.Fatalf("GetAllOnlineUUIDs: %v", err)
	}
	if _, listed := byUUID[uuids[0]]; listed {
		t.Fatal("a device with no live addresses was reported online")
	}
}
