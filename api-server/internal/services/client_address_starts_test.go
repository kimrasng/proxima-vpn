package services

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestClientAddressStartsSurviveExitSwitchAndIncompleteReport(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	user := uuid.NewString()
	t.Cleanup(func() {
		_ = rdb.Del(ctx, "account:"+user+":client_addresses", "account:"+user+":client_address_starts").Err()
	})
	obs := ClientAddressObservation{Complete: true, Addresses: []string{"203.0.113.2"}, Starts: map[string]int64{"203.0.113.2": 100}}
	if _, err := PreserveClientAddressStarts(ctx, rdb, user, obs); err != nil {
		t.Fatal(err)
	}
	obs.Starts["203.0.113.2"] = 200
	got, err := PreserveClientAddressStarts(ctx, rdb, user, obs)
	if err != nil || got.Starts["203.0.113.2"] != 100 {
		t.Fatalf("Exit switch reset start: %+v %v", got, err)
	}
	if _, err := PreserveClientAddressStarts(ctx, rdb, user, ClientAddressObservation{Complete: false}); err != nil {
		t.Fatal(err)
	}
	got, err = PreserveClientAddressStarts(ctx, rdb, user, obs)
	if err != nil || got.Starts["203.0.113.2"] != 100 {
		t.Fatal("incomplete epoch erased start")
	}
	if _, err := PreserveClientAddressStarts(ctx, rdb, user, ClientAddressObservation{Complete: true, Addresses: []string{}, Starts: map[string]int64{}}); err != nil {
		t.Fatal(err)
	}
	got, err = PreserveClientAddressStarts(ctx, rdb, user, obs)
	if err != nil || got.Starts["203.0.113.2"] != 200 {
		t.Fatal("confirmed offline transition retained old start")
	}
}
