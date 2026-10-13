package services

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestAddressReleaseRequiresFreshDistinctEpochsAndAdmissionFence(t *testing.T) {
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
	source := "203.0.113.2"
	reserve := func() {
		t.Helper()
		ok, err := ReserveObservedClientAddress(ctx, rdb, account, source, 1)
		if err != nil || !ok {
			t.Fatalf("reserve %v %v", ok, err)
		}
	}
	capture := func() AddressReservationFence {
		t.Helper()
		f, err := CaptureAddressReservationFence(ctx, rdb, account)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	epoch := time.Now().UnixMilli() - 10
	empty := ClientAddressObservation{Complete: true, Addresses: []string{}, SnapshotID: "snapshot-1"}
	reserve()
	fence := capture()
	run := func(obs ClientAddressObservation, stamp int64, f AddressReservationFence) []string {
		t.Helper()
		out, err := ReconcileAbsentClientAddresses(ctx, rdb, account, obs, stamp, f)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if len(run(empty, epoch, fence)) != 0 {
		t.Fatal("single absence released slot")
	}
	// Advance only the first sample's age; later epochs remain real server time.
	age := func() {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"sequence": fence[source], "since": epoch - 31000, "snapshot": empty.SnapshotID})
		if err := rdb.HSet(ctx, key+":absence", source, raw).Err(); err != nil {
			t.Fatal(err)
		}
	}
	age()
	if len(run(empty, epoch, fence)) != 0 {
		t.Fatal("replayed epoch released slot")
	}
	// Concurrent new admission invalidates pre-observation sequence.
	reserve()
	if len(run(empty, epoch+1, fence)) != 0 {
		t.Fatal("stale admission fence released slot")
	}
	fence = capture()
	empty.SnapshotID = "snapshot-early"
	run(empty, epoch+2, fence)
	empty.SnapshotID = "snapshot-1"
	if len(run(empty, epoch+3, fence)) != 0 {
		t.Fatal("different report snapshot released before 30 seconds")
	}
	age()
	// Even an incomplete collection with a reused epoch must clear evidence.
	run(ClientAddressObservation{Complete: false}, epoch+3, fence)
	if len(run(empty, epoch+4, fence)) != 0 {
		t.Fatal("incomplete observation retained absence")
	}
	age()
	unidentified := empty
	unidentified.SnapshotID = ""
	if len(run(unidentified, epoch+4, fence)) != 0 || rdb.HLen(ctx, key+":absence").Val() != 0 {
		t.Fatal("unidentified observation retained absence")
	}
	run(empty, epoch+5, fence)
	age()
	if len(run(empty, epoch+6, fence)) != 0 {
		t.Fatal("reused report snapshot released slot")
	}
	empty.SnapshotID = "snapshot-2"
	got := run(empty, epoch+7, fence)
	if len(got) != 1 || got[0] != source {
		t.Fatalf("confirmed absence not released: %v", got)
	}
	if rdb.SIsMember(ctx, key, source).Val() {
		t.Fatal("released slot retained")
	}
	if _, err := ReconcileAbsentClientAddresses(ctx, rdb, account, empty, time.Now().Add(time.Minute).UnixMilli(), fence); err == nil {
		t.Fatal("future epoch accepted")
	}
}
