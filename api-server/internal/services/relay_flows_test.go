package services

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func TestRelayFlowReportsDistinguishEmptyUnknownAndMalformed(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	node := uuid.NewString()
	key := "node:" + node + ":relay_flows"
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })
	flow := nodeprov.RelayFlow{PeerIP: "10.0.2.1", PeerPort: 40000, ExitIP: "10.0.2.2", ExitPort: 8443, ClientIP: "203.0.113.2"}
	if err := PublishRelayFlows(ctx, rdb, node, nodeprov.RelayFlowReport{Known: true, Flows: []nodeprov.RelayFlow{flow}}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRelayFlows(ctx, rdb, node)
	if err != nil || len(got.Flows) != 1 || got.Flows[0] != flow {
		t.Fatalf("snapshot: %+v %v", got, err)
	}
	if ttl := rdb.TTL(ctx, key).Val(); ttl <= 0 || ttl > RelayFlowFreshness {
		t.Fatalf("unbounded report freshness: %v", ttl)
	}
	if err := PublishRelayFlows(ctx, rdb, node, nodeprov.RelayFlowReport{Known: true}); err != nil {
		t.Fatal(err)
	}
	got, err = ReadRelayFlows(ctx, rdb, node)
	if err != nil || !got.Known || got.Flows == nil || len(got.Flows) != 0 {
		t.Fatalf("explicit empty: %+v %v", got, err)
	}
	if err := PublishRelayFlows(ctx, rdb, node, nodeprov.RelayFlowReport{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRelayFlows(ctx, rdb, node); err == nil {
		t.Fatal("unknown reported as empty")
	}
	if err := PublishRelayFlows(ctx, rdb, node, nodeprov.RelayFlowReport{Known: true, Flows: []nodeprov.RelayFlow{flow}}); err != nil {
		t.Fatal(err)
	}
	flow.ClientIP = "invalid"
	if err := PublishRelayFlows(ctx, rdb, node, nodeprov.RelayFlowReport{Known: true, Flows: []nodeprov.RelayFlow{flow}}); err == nil {
		t.Fatal("accepted invalid flow")
	}
	if _, err := ReadRelayFlows(ctx, rdb, node); err == nil {
		t.Fatal("malformed report retained stale evidence")
	}
}
