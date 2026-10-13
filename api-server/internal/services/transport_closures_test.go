package services

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func TestTransportClosureQueuePreservesExactObservedOwnership(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	node := uuid.NewString()
	key := "node:" + node + ":transport_closures"
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })
	connection := nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100, IdleMillis: 50}
	id, err := QueueTransportClosure(ctx, rdb, node, connection)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := PendingTransportClosures(ctx, rdb, node)
	if err != nil || len(commands) != 1 || commands[0].ID != id || commands[0].Connection != connection {
		t.Fatalf("commands=%+v err=%v", commands, err)
	}
	if ttl := rdb.TTL(ctx, key).Val(); ttl <= 0 {
		t.Fatalf("unbounded command ttl: %v", ttl)
	}
	connection.UUID = "invalid"
	if _, err := QueueTransportClosure(ctx, rdb, node, connection); err == nil {
		t.Fatal("invalid ownership queued")
	}
}
