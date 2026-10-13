package services

import (
	"context"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
	"os"
	"sync"
	"testing"
)

func TestSessionReservationCountsSameCredentialAndAddressSeparately(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	account := uuid.NewString()
	key := "account:" + account + ":tunnel_session_slots"
	t.Cleanup(func() { _ = rdb.Del(ctx, key, key+":sequence", key+":absence", key+":epoch").Err() })
	first := ObservedTunnelSession{ExitID: uuid.NewString(), Connection: nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}}
	second := first
	second.Connection.PeerPort++
	second.Connection.AcceptedAt++
	sessions := []ObservedTunnelSession{first, second}
	results := make([]bool, 2)
	var wg sync.WaitGroup
	for n := range sessions {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			allowed, err := ReserveTunnelSession(ctx, rdb, account, sessions[n], 1)
			if err != nil {
				t.Error(err)
			}
			results[n] = allowed
		}(n)
	}
	wg.Wait()
	if results[0] == results[1] {
		t.Fatalf("one session must win capacity: %v", results)
	}
	winner := first
	if results[1] {
		winner = second
	}
	for n := 0; n < 3; n++ {
		allowed, err := ReserveTunnelSession(ctx, rdb, account, winner, 1)
		if err != nil || !allowed {
			t.Fatalf("idempotent reservation: %v %v", allowed, err)
		}
	}
	if ttl := rdb.TTL(ctx, key).Val(); ttl != -1 {
		t.Fatalf("session reservations must not expire silently: %v", ttl)
	}
}

func TestSessionKeyFencesTupleReuseAndExitIdentity(t *testing.T) {
	first := ObservedTunnelSession{ExitID: uuid.NewString(), Connection: nodeprov.TunnelConnection{UUID: uuid.NewString(), PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}}
	key, err := TunnelSessionKey(first)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := TunnelSessionKey(first)
	if err != nil || key != retry {
		t.Fatal("same session changed key")
	}
	reused := first
	reused.Connection.AcceptedAt++
	changed, err := TunnelSessionKey(reused)
	if err != nil || changed == key {
		t.Fatal("reused tuple shared a slot")
	}
	reused = first
	reused.ExitID = uuid.NewString()
	changed, err = TunnelSessionKey(reused)
	if err != nil || changed == key {
		t.Fatal("different Exit shared a slot")
	}
}
