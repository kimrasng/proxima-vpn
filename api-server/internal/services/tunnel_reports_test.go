package services

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func TestTunnelReportsInvalidateUnknownAndMalformedEvidence(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	node := uuid.NewString()
	key := "node:" + node + ":tunnels"
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })
	connection := nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 1, IdleMillis: 100}
	good := nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{connection}}
	if err := PublishTunnelReport(ctx, rdb, node, good); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTunnelReport(ctx, rdb, node)
	if err != nil || len(got.Connections) != 1 || got.Connections[0] != connection {
		t.Fatalf("report: %+v %v", got, err)
	}
	if ttl := rdb.TTL(ctx, key).Val(); ttl <= 0 || ttl > TunnelReportFreshness {
		t.Fatalf("TTL=%v", ttl)
	}
	if err := PublishTunnelReport(ctx, rdb, node, nodeprov.TunnelReport{Known: true}); err != nil {
		t.Fatal(err)
	}
	got, err = ReadTunnelReport(ctx, rdb, node)
	if err != nil || !got.Known || got.Connections == nil || len(got.Connections) != 0 {
		t.Fatalf("empty report: %+v %v", got, err)
	}
	if err := PublishTunnelReport(ctx, rdb, node, nodeprov.TunnelReport{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTunnelReport(ctx, rdb, node); err == nil {
		t.Fatal("unknown became empty")
	}
	if err := PublishTunnelReport(ctx, rdb, node, good); err != nil {
		t.Fatal(err)
	}
	connection.UUID = "invalid"
	if err := PublishTunnelReport(ctx, rdb, node, nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{connection}}); err == nil {
		t.Fatal("accepted invalid credential")
	}
	if _, err := ReadTunnelReport(ctx, rdb, node); err == nil {
		t.Fatal("malformed observation kept old evidence")
	}
}
