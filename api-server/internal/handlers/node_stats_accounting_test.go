package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestCanonicalStats(t *testing.T) {
	a := crypto.NewUUID()
	b := crypto.NewUUID()
	first, err := canonicalStats([]statEntry{{XrayUUID: a, UpBytes: 3}, {XrayUUID: b, DnBytes: 4}})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := canonicalStats([]statEntry{{XrayUUID: b, DnBytes: 4}, {XrayUUID: a, UpBytes: 3}})
	if err != nil || first != reordered {
		t.Fatalf("same entries should hash identically: %s %s %v", first, reordered, err)
	}
	changed, err := canonicalStats([]statEntry{{XrayUUID: a, UpBytes: 4}, {XrayUUID: b, DnBytes: 4}})
	if err != nil || first == changed {
		t.Fatalf("different bytes should have a distinct hash: %v", err)
	}
	for _, entries := range [][]statEntry{
		{{XrayUUID: a, UpBytes: -1}},
		{{XrayUUID: a, UpBytes: 1}, {XrayUUID: a, DnBytes: 1}},
		{{XrayUUID: "invalid", UpBytes: 1}},
	} {
		if _, err := canonicalStats(entries); !errors.Is(err, errInvalidStats) {
			t.Fatalf("invalid stats accepted: %+v, err=%v", entries, err)
		}
	}
}

func TestAccountStatsIdempotentAndAtomic(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	nodeID := seedChainNode(t, pool, "account-batch-"+crypto.NewUUID(), "exit", 443)
	groupID := seedChainGroup(t, pool, "account-group-"+crypto.NewUUID())
	var planID, userID, deviceID string
	if err := pool.QueryRow(ctx, `INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		VALUES ($1, 30, 2, $2) RETURNING id::text`, "batch-plan-"+crypto.NewUUID(), groupID).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, planID) })
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, sub_token, plan_id)
		VALUES ($1, 'unused', $2, $3) RETURNING id::text`, "batch-"+crypto.NewUUID()+"@example.test", crypto.NewUUID(), planID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) })
	deviceUUID := crypto.NewUUID()
	if err := pool.QueryRow(ctx, `INSERT INTO devices (user_id, xray_uuid) VALUES ($1, $2) RETURNING id::text`, userID, deviceUUID).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	batchID := crypto.NewUUID()
	stats := []statEntry{{XrayUUID: deviceUUID, UpBytes: 100, DnBytes: 50}}
	duplicate, err := accountStats(ctx, pool, nodeID, batchID, stats)
	if err != nil || duplicate {
		t.Fatalf("first account: duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = accountStats(ctx, pool, nodeID, batchID, stats)
	if err != nil || !duplicate {
		t.Fatalf("replay: duplicate=%v err=%v", duplicate, err)
	}
	if _, err := accountStats(ctx, pool, nodeID, batchID, []statEntry{{XrayUUID: deviceUUID, UpBytes: 200}}); !errors.Is(err, errBatchConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	var used int64
	var logs, batches int
	if err := pool.QueryRow(ctx, `SELECT traffic_used FROM users WHERE id=$1`, userID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM traffic_logs WHERE node_id=$1 AND device_id=$2`, nodeID, deviceID).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM traffic_batches WHERE node_id=$1`, nodeID).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if used != 150 || logs != 1 || batches != 1 {
		t.Fatalf("used=%d logs=%d batches=%d", used, logs, batches)
	}
	if _, err := accountStats(ctx, pool, nodeID, crypto.NewUUID(), []statEntry{{XrayUUID: deviceUUID, UpBytes: 5}, {XrayUUID: crypto.NewUUID(), DnBytes: 10}}); !errors.Is(err, errUnknownDevice) {
		t.Fatalf("unknown device should rollback entire batch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT traffic_used FROM users WHERE id=$1`, userID).Scan(&used); err != nil || used != 150 {
		t.Fatalf("rollback failed: used=%d err=%v", used, err)
	}
}
