package services

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestTunnelSlotReleaseRequiresIndependentCompleteFleetReports(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	account := uuid.NewString()
	key := TunnelSlotKey(account)
	t.Cleanup(func() { _ = rdb.Del(ctx, key, key+":sequence", key+":absence", key+":epoch").Err() })
	session := ObservedTunnelSession{ExitID: uuid.NewString(), Connection: nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}}
	reserved, err := ReserveTunnelSession(ctx, rdb, account, session, 1)
	if err != nil || !reserved {
		t.Fatal(err)
	}
	fence, err := CaptureTunnelSessionFence(ctx, rdb, account)
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Now().UnixMilli() - 10
	empty := TunnelSessionObservation{Complete: true, Sessions: []ObservedTunnelSession{}}
	run := func(observation TunnelSessionObservation, snapshot string, stamp int64, seen map[string]string) []string {
		t.Helper()
		out, err := ReconcileAbsentTunnelSessions(ctx, rdb, account, observation, snapshot, stamp, seen)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if len(run(empty, "snapshot-1", epoch, fence)) != 0 {
		t.Fatal("single absence released slot")
	}
	id, _ := TunnelSessionKey(session)
	age := func() {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"sequence": fence[id], "since": epoch - 31000, "snapshot": "snapshot-1"})
		if err := rdb.HSet(ctx, key+":absence", id, raw).Err(); err != nil {
			t.Fatal(err)
		}
	}
	age()
	if len(run(empty, "snapshot-1", epoch+1, fence)) != 0 {
		t.Fatal("reused snapshot released slot")
	}
	reserved, err = ReserveTunnelSession(ctx, rdb, account, session, 1)
	if err != nil || !reserved {
		t.Fatal(err)
	}
	if len(run(empty, "snapshot-2", epoch+2, fence)) != 0 {
		t.Fatal("stale admission fence released slot")
	}
	fence, err = CaptureTunnelSessionFence(ctx, rdb, account)
	if err != nil {
		t.Fatal(err)
	}
	run(empty, "snapshot-3", epoch+3, fence)
	age() // old snapshot/sequence must not survive incomplete evidence
	run(TunnelSessionObservation{Complete: false}, "", epoch+4, fence)
	if len(run(empty, "snapshot-4", epoch+5, fence)) != 0 {
		t.Fatal("incomplete report retained absence")
	}
	raw, _ := json.Marshal(map[string]any{"sequence": fence[id], "since": epoch - 31000, "snapshot": "snapshot-4"})
	if err := rdb.HSet(ctx, key+":absence", id, raw).Err(); err != nil {
		t.Fatal(err)
	}
	got := run(empty, "snapshot-5", epoch+6, fence)
	if len(got) != 1 || got[0] != id {
		t.Fatalf("confirmed absence not released: %v", got)
	}
}
