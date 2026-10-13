package services

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func releaseTestRedis(t *testing.T) (*redis.Client, context.Context) {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, context.Background()
}

func TestTunnelSlotReleaseRequiresIndependentCompleteFleetReports(t *testing.T) {
	rdb, ctx := releaseTestRedis(t)
	account := uuid.NewString()
	key := TunnelSlotKey(account)
	t.Cleanup(func() { _ = rdb.Del(ctx, key, key+":sequence", key+":absence", key+":epoch").Err() })
	session := ObservedTunnelSession{ExitID: uuid.NewString(), Connection: nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}}
	reserved, err := ReserveTunnelSession(ctx, rdb, account, session, 1)
	if err != nil || !reserved {
		t.Fatalf("reserve=%v err=%v", reserved, err)
	}
	fence, err := CaptureTunnelSessionFence(ctx, rdb, account)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := rdb.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	now := clock.UnixMilli()
	empty := TunnelSessionObservation{Complete: true, Sessions: []ObservedTunnelSession{}}
	receipt := func(id string, at int64) map[string]tunnelReportReceipt {
		return map[string]tunnelReportReceipt{session.ExitID: {ReportID: id, ReceivedAt: at}}
	}
	// Use increasing Redis-controlled epochs; elapsed time is determined by
	// the receipt timestamps and this integration test itself does not sleep.
	stamp := now - 1000
	run := func(obs TunnelSessionObservation, receipts map[string]tunnelReportReceipt) []string {
		t.Helper()
		stamp++
		if err := rdb.Set(ctx, key+":epoch", stamp-1, 0).Err(); err != nil {
			t.Fatal(err)
		}
		out, err := ReconcileAbsentTunnelSessionsWithReceipts(ctx, rdb, account, obs, receiptSnapshotID(receipts), stamp, fence, receipts)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := receipt("report-1", now-2000)
	if got := run(empty, first); len(got) != 0 {
		t.Fatalf("single absence released slot: %v", got)
	}
	if got := run(empty, first); len(got) != 0 {
		t.Fatalf("same report receipt released slot: %v", got)
	}
	if got := run(empty, receipt("report-2", now)); len(got) != 0 {
		t.Fatalf("report intervals under 30 seconds released slot: %v", got)
	}
	id, err := TunnelSessionKey(session)
	if err != nil {
		t.Fatal(err)
	}
	if exists := rdb.SIsMember(ctx, key, id).Val(); !exists {
		t.Fatal("short receipt interval released slot")
	}
	// Reset the monotonic per-test epoch for an isolated receipt-age case.
	if err := rdb.Set(ctx, key+":epoch", now-1, 0).Err(); err != nil {
		t.Fatal(err)
	}
	longOld := receipt("report-old", now-31_000)
	absence, _ := json.Marshal(map[string]any{"sequence": fence[id], "since": now - 31_000, "snapshot": receiptSnapshotID(longOld), "receipts": longOld})
	if err := rdb.Set(ctx, key+":epoch", now-1, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.HSet(ctx, key+":absence", id, absence).Err(); err != nil {
		t.Fatal(err)
	}
	fresh := receipt("report-new", now)
	got, err := ReconcileAbsentTunnelSessionsWithReceipts(ctx, rdb, account, empty, receiptSnapshotID(fresh), now, fence, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != id {
		t.Fatalf("independent receipts >=30s apart did not release: %v", got)
	}
}

func TestObservedTunnelSessionKeysDeduplicatesExactTransports(t *testing.T) {
	exit := uuid.NewString()
	connection := nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100, IdleMillis: 1}
	idleChanged := connection
	idleChanged.IdleMillis = 9000
	newTransport := connection
	newTransport.PeerPort++
	newTransport.IdleMillis = 2
	observation := TunnelSessionObservation{Complete: true, Sessions: []ObservedTunnelSession{
		{ExitID: exit, Connection: connection},
		{ExitID: exit, Connection: idleChanged}, // idle activity is not session identity
		{ExitID: exit, Connection: newTransport},
	}}
	keys, err := observedTunnelSessionKeys(observation)
	if err != nil {
		t.Fatal(err)
	}
	first, err := TunnelSessionKey(ObservedTunnelSession{ExitID: exit, Connection: connection})
	if err != nil {
		t.Fatal(err)
	}
	second, err := TunnelSessionKey(ObservedTunnelSession{ExitID: exit, Connection: newTransport})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{first, second}
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys=%v want=%v", keys, want)
	}
}

func TestObservedTunnelSessionKeysRejectConflictingTransportOwnership(t *testing.T) {
	exit := uuid.NewString()
	connection := nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100, IdleMillis: 1}
	conflicting := connection
	conflicting.UUID = uuid.NewString()
	newGeneration := connection
	newGeneration.AcceptedAt++
	for name, sessions := range map[string][]ObservedTunnelSession{
		"same tuple different credential": {{ExitID: exit, Connection: connection}, {ExitID: exit, Connection: conflicting}},
		"same tuple different generation": {{ExitID: exit, Connection: connection}, {ExitID: exit, Connection: newGeneration}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := observedTunnelSessionKeys(TunnelSessionObservation{Complete: true, Sessions: sessions}); err == nil {
				t.Fatal("conflicting transport owner accepted")
			}
		})
	}
}

func TestReceiptAwareReconciliationRejectsMissingStaleAndMismatchedEvidence(t *testing.T) {
	rdb, ctx := releaseTestRedis(t)
	account := uuid.NewString()
	key := TunnelSlotKey(account)
	t.Cleanup(func() { _ = rdb.Del(ctx, key, key+":sequence", key+":absence", key+":epoch").Err() })
	clock, err := rdb.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	now := clock.UnixMilli()
	obs := TunnelSessionObservation{Complete: true, Sessions: []ObservedTunnelSession{}}
	receipts := map[string]tunnelReportReceipt{uuid.NewString(): {ReportID: "report-1", ReceivedAt: now}}
	fence := map[string]string{}
	for name, tc := range map[string]struct {
		snapshot string
		receipts map[string]tunnelReportReceipt
	}{
		"missing":             {snapshot: receiptSnapshotID(receipts)},
		"mismatched identity": {snapshot: "wrong", receipts: receipts},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReconcileAbsentTunnelSessionsWithReceipts(ctx, rdb, account, obs, tc.snapshot, now, fence, tc.receipts); err == nil {
				t.Fatal("invalid receipt evidence accepted")
			}
		})
	}
	stale := map[string]tunnelReportReceipt{}
	for node, r := range receipts {
		r.ReceivedAt = now - TunnelReportFreshness.Milliseconds() - 1
		stale[node] = r
	}
	if _, err := ReconcileAbsentTunnelSessionsWithReceipts(ctx, rdb, account, obs, receiptSnapshotID(stale), now, fence, stale); err == nil {
		t.Fatal("stale report receipt accepted")
	}
	future := map[string]tunnelReportReceipt{}
	for node, r := range receipts {
		r.ReceivedAt = now + 1
		future[node] = r
	}
	if _, err := ReconcileAbsentTunnelSessionsWithReceipts(ctx, rdb, account, obs, receiptSnapshotID(future), now, fence, future); err == nil {
		t.Fatal("future report receipt accepted")
	}
}

func TestSameTunnelReceiptSetRequiresExactFleetIdentity(t *testing.T) {
	a := map[string]tunnelReportReceipt{"exit-a": {ReportID: "one", ReceivedAt: 1000}, "exit-b": {ReportID: "two", ReceivedAt: 1000}}
	if !sameTunnelReceipts(a, map[string]tunnelReportReceipt{"exit-b": {ReportID: "two", ReceivedAt: 1000}, "exit-a": {ReportID: "one", ReceivedAt: 1000}}) {
		t.Fatal("map order changed identical receipt set")
	}
	for name, b := range map[string]map[string]tunnelReportReceipt{
		"different report":     {"exit-a": {ReportID: "changed", ReceivedAt: 2000}, "exit-b": {ReportID: "two", ReceivedAt: 1000}},
		"missing exit":         {"exit-a": {ReportID: "one", ReceivedAt: 1000}},
		"changed receipt time": {"exit-a": {ReportID: "one", ReceivedAt: 2000}, "exit-b": {ReportID: "two", ReceivedAt: 1000}},
	} {
		t.Run(name, func(t *testing.T) {
			if sameTunnelReceipts(a, b) {
				t.Fatal("changed fleet receipt set treated as identical")
			}
		})
	}
}
