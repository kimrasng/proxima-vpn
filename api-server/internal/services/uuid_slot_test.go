package services

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestReserveAccountUUIDSlotAcrossExits(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstExit := redis.NewClient(&redis.Options{Addr: addr})
	secondExit := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = firstExit.Close() }()
	defer func() { _ = secondExit.Close() }()
	if err := firstExit.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}
	userID := uuid.NewString()
	key := AccountUUIDSlotKey(userID)
	defer firstExit.Del(context.Background(), key)

	uuids := []string{uuid.NewString(), uuid.NewString()}
	results := make([]bool, 2)
	failures := make([]error, 2)
	var wg sync.WaitGroup
	for i, rdb := range []*redis.Client{firstExit, secondExit} {
		wg.Add(1)
		go func(i int, rdb *redis.Client) {
			defer wg.Done()
			results[i], failures[i] = ReserveAccountUUIDSlot(ctx, rdb, userID, uuids[i], 1)
		}(i, rdb)
	}
	wg.Wait()
	if failures[0] != nil || failures[1] != nil || results[0] == results[1] {
		t.Fatalf("atomic admission: allowed=%v failures=%v", results, failures)
	}
	winner, loser := 0, 1
	if results[1] {
		winner, loser = 1, 0
	}
	for _, tc := range []struct {
		device string
		cap    int
		want   bool
	}{
		{uuids[winner], 0, true}, // Unlimited plan admits without touching existing slot.
		{uuids[loser], 1, false},
	} {
		got, err := ReserveAccountUUIDSlot(ctx, secondExit, userID, tc.device, tc.cap)
		if err != nil || got != tc.want {
			t.Fatalf("reserve(%s, %d) = %v, %v; want %v", tc.device, tc.cap, got, err, tc.want)
		}
	}
	if ttl := firstExit.TTL(ctx, key).Val(); ttl != -1 {
		t.Fatalf("idle admitted UUID must not expire: TTL = %v", ttl)
	}
}
