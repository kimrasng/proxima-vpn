package services

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestOnlineReportEmptyClearsImmediatelyAndNodeSwitchKeepsStart(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	tracker := NewOnlineTracker(rdb)
	a, b := "test-online-exit-a", "test-online-exit-b"
	uuid, user := "test-online-uuid", "test-online-user"
	defer rdb.Del(ctx, "node:"+a+":online", "node:"+a+":online_ips", "node:"+a+":online_report", "node:"+b+":online", "node:"+b+":online_ips", "node:"+b+":online_report", "device:"+uuid+":online_since", "account:"+user+":online_uuids")
	if err := tracker.PublishOnlineReport(ctx, a, []string{uuid}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ObserveSessionStarts(ctx, user, []string{uuid}, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := tracker.PublishOnlineReport(ctx, b, []string{uuid}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tracker.PublishOnlineReport(ctx, a, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := tracker.GetOnlineUsers(ctx, a); err != nil || len(got) != 0 {
		t.Fatalf("empty report: %v %v", got, err)
	}
	if err := tracker.ObserveSessionStarts(ctx, user, []string{uuid}, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	starts, err := tracker.GetSessionStarts(ctx, []string{uuid})
	if err != nil || starts[uuid] != 100 {
		t.Fatalf("node switch reset start: %v %v", starts, err)
	}
	if err := tracker.PublishOnlineReport(ctx, b, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ObserveSessionStarts(ctx, user, nil, time.Unix(300, 0)); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ObserveSessionStarts(ctx, user, []string{uuid}, time.Unix(400, 0)); err != nil {
		t.Fatal(err)
	}
	starts, err = tracker.GetSessionStarts(ctx, []string{uuid})
	if err != nil || starts[uuid] != 400 {
		t.Fatalf("offline transition not reset: %v %v", starts, err)
	}
}
