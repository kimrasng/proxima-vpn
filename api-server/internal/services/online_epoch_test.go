package services

import (
	"context"
	"testing"
	"time"
)

func TestOnlineEpochAcrossExits(t *testing.T) {
	db, rdb, tracker, ctx := setupTracker(t)
	user, uuids := seedUserWithDevices(t, ctx, db, 2)
	a := seedNode(t, ctx, db, "epoch-a", false)
	b := seedNode(t, ctx, db, "epoch-b", false)
	// A relay is not an Exit and must not make the epoch incomplete.
	relay := seedNode(t, ctx, db, "epoch-relay", false)
	if _, err := db.Exec(ctx, `UPDATE nodes SET role='relay' WHERE id=$1`, relay); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	report := func(node string, ids ...string) {
		t.Helper()
		ips := map[string][]onlineIPFixture{}
		for _, id := range ids {
			ips[id] = []onlineIPFixture{{IP: "203.0.113.1", LastSeen: time.Now().Unix()}}
		}
		if err := tracker.PublishOnlineReport(ctx, node, ids, ips); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = rdb.Del(ctx, "node:"+node+":online", "node:"+node+":online_ips", "node:"+node+":online_report").Err()
		})
	}
	t.Cleanup(func() {
		_ = rdb.Del(ctx, "account:"+user+":online_uuids", "device:"+uuids[0]+":online_since", "device:"+uuids[1]+":online_since").Err()
	})
	report(a, uuids[0])
	obs, err := tracker.ObserveOnlineForUser(ctx, db, user, now)
	if err != nil || obs.Complete || len(obs.UnknownNodeIDs) != 1 || obs.UnknownNodeIDs[0] != b || len(obs.Starts) != 0 {
		t.Fatalf("missing Exit must be unknown: %+v %v", obs, err)
	}
	report(b, uuids[0])
	obs, err = tracker.ObserveOnlineForUser(ctx, db, user, now)
	if err != nil || !obs.Complete || len(obs.OnlineUUIDs) != 1 || obs.NewestUUID != uuids[0] {
		t.Fatalf("same UUID on two Exits: %+v %v", obs, err)
	}
	start := obs.Starts[uuids[0]]
	report(a)
	obs, err = tracker.ObserveOnlineForUser(ctx, db, user, now.Add(time.Second))
	if err != nil || !obs.Complete || obs.Starts[uuids[0]] != start {
		t.Fatalf("node switch reset age: %+v %v", obs, err)
	}
	// Even with an empty report on B, a stale Exit cannot prove offline.
	report(b)
	if _, err := db.Exec(ctx, `UPDATE nodes SET last_seen=NOW()-INTERVAL '30 minutes' WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	obs, err = tracker.ObserveOnlineForUser(ctx, db, user, now)
	if err != nil || obs.Complete || len(obs.Starts) != 0 {
		t.Fatalf("stale Exit accepted: %+v %v", obs, err)
	}
	starts, err := tracker.GetSessionStarts(ctx, uuids[:1])
	if err != nil || starts[uuids[0]] != start {
		t.Fatalf("incomplete epoch erased age: %v %v", starts, err)
	}
	if _, err := db.Exec(ctx, `UPDATE nodes SET last_seen=NOW() WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	obs, err = tracker.ObserveOnlineForUser(ctx, db, user, now.Add(2*time.Second))
	if err != nil || !obs.Complete || len(obs.OnlineUUIDs) != 0 {
		t.Fatalf("empty reports not authoritative: %+v %v", obs, err)
	}
	report(a, uuids[0], uuids[1])
	obs, err = tracker.ObserveOnlineForUser(ctx, db, user, now.Add(3*time.Second))
	if err != nil || !obs.Complete || !obs.Ambiguous || obs.NewestUUID != "" || len(obs.AmbiguousUUIDs) != 2 || obs.Starts[uuids[0]] <= start {
		t.Fatalf("simultaneous transitions must have no victim: %+v %v", obs, err)
	}
}

func TestSessionStartsConcurrentSweeps(t *testing.T) {
	_, rdb, tracker, ctx := setupTracker(t)
	user, uuid := "epoch-concurrent-user", "epoch-concurrent-uuid"
	t.Cleanup(func() { _ = rdb.Del(ctx, "account:"+user+":online_uuids", "device:"+uuid+":online_since").Err() })
	if err := tracker.ObserveSessionStarts(ctx, user, []string{uuid}, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ObserveSessionStarts(ctx, user, []string{uuid}, time.Unix(20, 0)); err != nil {
		t.Fatal(err)
	}
	start, err := tracker.GetSessionStarts(context.Background(), []string{uuid})
	if err != nil || start[uuid] != 10 {
		t.Fatalf("start moved: %v %v", start, err)
	}
	// Explicit complete empty set, not a missing report, proves offline.
	if err := tracker.ObserveSessionStarts(ctx, user, nil, time.Unix(30, 0)); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ObserveSessionStarts(ctx, user, []string{uuid}, time.Unix(40, 0)); err != nil {
		t.Fatal(err)
	}
	start, err = tracker.GetSessionStarts(ctx, []string{uuid})
	if err != nil || start[uuid] != 40 {
		t.Fatalf("start not reset: %v %v", start, err)
	}
}
