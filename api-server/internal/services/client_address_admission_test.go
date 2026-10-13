package services

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestObservedAddressAdmissionIsAtomicAcrossExits(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	account := uuid.NewString()
	t.Cleanup(func() { _ = rdb.Del(ctx, "account:"+account+":observed_address_slots").Err() })
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, source := range []string{"203.0.113.2", "203.0.113.3"} {
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			allowed, err := ReserveObservedClientAddress(ctx, rdb, account, source, 1)
			if err != nil {
				t.Error(err)
			}
			results <- allowed
		}(source)
	}
	wg.Wait()
	close(results)
	admitted := 0
	for allowed := range results {
		if allowed {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatalf("admitted=%d want1", admitted)
	}
	members, err := rdb.SMembers(ctx, "account:"+account+":observed_address_slots").Result()
	if err != nil || len(members) != 1 {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		allowed, err := ReserveObservedClientAddress(ctx, rdb, account, members[0], 1)
		if err != nil || !allowed {
			t.Fatalf("same address consumed another slot: %v %v", allowed, err)
		}
	}
}
