package services

import (
	"testing"
	"time"
)

func TestOnlineUUIDUnionCountsOnceAcrossExitsAndExcludesEntry(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	user, uuids := seedUserWithDevices(t, ctx, pool, 2)
	exitA := seedNode(t, ctx, pool, "uuid-union-a", false)
	exitB := seedNode(t, ctx, pool, "uuid-union-b", false)
	entry := seedNode(t, ctx, pool, "uuid-union-entry", false)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET role='relay' WHERE id=$1`, entry); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, node := range []string{exitA, exitB, entry} {
		ids := []string{uuids[0]}
		if node == entry {
			ids = []string{uuids[1]}
		}
		ips := map[string][]onlineIPFixture{ids[0]: {{IP: "203.0.113.1", LastSeen: now}}}
		if err := tracker.PublishOnlineReport(ctx, node, ids, ips); err != nil {
			t.Fatal(err)
		}
		n := node
		t.Cleanup(func() {
			_ = rdb.Del(ctx, "node:"+n+":online", "node:"+n+":online_ips", "node:"+n+":online_report").Err()
		})
	}
	got, err := tracker.CountOnlineForUser(ctx, pool, user)
	if err != nil || got != 1 {
		t.Fatalf("union count=%d err=%v, want 1", got, err)
	}
	ips := map[string][]onlineIPFixture{uuids[1]: {{IP: "203.0.113.1", LastSeen: now}}}
	if err := tracker.PublishOnlineReport(ctx, exitB, []string{uuids[1]}, ips); err != nil {
		t.Fatal(err)
	}
	got, err = tracker.CountOnlineForUser(ctx, pool, user)
	if err != nil || got != 2 {
		t.Fatalf("two UUIDs behind same IP count=%d err=%v", got, err)
	}
	if err := tracker.PublishOnlineReport(ctx, exitB, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err = tracker.CountOnlineForUser(ctx, pool, user)
	if err != nil || got != 1 {
		t.Fatalf("empty exit report count=%d err=%v", got, err)
	}
}
