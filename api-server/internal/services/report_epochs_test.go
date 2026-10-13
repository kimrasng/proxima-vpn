package services

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func TestReportReceiptSnapshotIdentityMatchesAccountSnapshot(t *testing.T) {
	receipts := map[string]tunnelReportReceipt{
		"exit-a": {ReportID: "receipt-1", ReceivedAt: 1000},
		"exit-b": {ReportID: "receipt-2", ReceivedAt: 1200},
	}
	want := clientAddressSnapshotID([]string{"exit-a:receipt-1", "exit-b:receipt-2"})
	if got := receiptSnapshotID(receipts); got != want {
		t.Fatalf("receipt identity mismatch: %s != %s", got, want)
	}
}

func TestReportEpochIdentity(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	for _, kind := range []string{"entry", "exit"} {
		t.Run(kind, func(t *testing.T) {
			node := uuid.NewString()
			key := "node:" + node + ":tunnels"
			publish := func() error { return PublishTunnelReport(ctx, rdb, node, nodeprov.TunnelReport{Known: true}) }
			read := func() (string, error) { _, id, err := ReadTunnelReportWithID(ctx, rdb, node); return id, err }
			legacy := `{"Known":true,"Connections":[]}`
			if kind == "entry" {
				key = "node:" + node + ":relay_flows"
				publish = func() error { return PublishRelayFlows(ctx, rdb, node, nodeprov.RelayFlowReport{Known: true}) }
				read = func() (string, error) { _, id, err := ReadRelayFlowsWithID(ctx, rdb, node); return id, err }
				legacy = `{"Known":true,"Flows":[]}`
			}
			t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })
			if err := publish(); err != nil {
				t.Fatal(err)
			}
			first, err := read()
			if err != nil || first == "" {
				t.Fatalf("identity %q: %v", first, err)
			}
			repeated, err := read()
			if err != nil || repeated != first {
				t.Fatalf("reused snapshot changed identity: %q %v", repeated, err)
			}
			if err := publish(); err != nil {
				t.Fatal(err)
			}
			second, err := read()
			if err != nil || second == "" || second == first {
				t.Fatalf("new receipt reused identity: %q %v", second, err)
			}
			if err := rdb.Set(ctx, key, legacy, TunnelReportFreshness).Err(); err != nil {
				t.Fatal(err)
			}
			if _, err := read(); err == nil {
				t.Fatal("legacy report treated as known")
			}
			if err := rdb.Del(ctx, key).Err(); err != nil {
				t.Fatal(err)
			}
			if _, err := read(); err == nil {
				t.Fatal("missing report treated as known")
			}
		})
	}
}

func TestClientAddressSnapshotFingerprint(t *testing.T) {
	first := clientAddressSnapshotID([]string{"exit:a:1", "entry:b:2"})
	if first != clientAddressSnapshotID([]string{"entry:b:2", "exit:a:1"}) {
		t.Fatal("report order changed snapshot identity")
	}
	for _, ids := range [][]string{{"exit:a:3", "entry:b:2"}, {"exit:a:1", "entry:b:3"}, {"exit:a:1"}} {
		if first == clientAddressSnapshotID(ids) {
			t.Fatal("report change did not change snapshot identity")
		}
	}
}
