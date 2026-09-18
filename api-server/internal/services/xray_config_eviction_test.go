package services

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedEvictionFixture builds a node in a group, a plan pointing at that group,
// a user on the plan, and one device - the minimum for generate() to emit a
// client for that device.
func seedEvictionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (nodeID, deviceUUID string) {
	t.Helper()
	tag := fmt.Sprintf("evict-%d", os.Getpid())

	var groupID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, tag,
	).Scan(&groupID); err != nil {
		t.Fatalf("seed node group: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, ip, api_key, status, last_seen)
		 VALUES ($1, '198.51.100.10', $2, 'online', NOW()) RETURNING id::text`,
		tag, tag+"-key",
	).Scan(&nodeID); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`,
		groupID, nodeID,
	); err != nil {
		t.Fatalf("attach node to group: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO inbounds (node_id, protocol, port, tag, settings)
		 VALUES ($1, 'vless_reality', 8443, 'vless-in', '{}'::jsonb)`,
		nodeID,
	); err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	var planID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		 VALUES ($1, 30, 5, $2) RETURNING id::text`, tag, groupID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, plan_id, status, is_active, sub_token)
		 VALUES ($1, 'x', $2, 'active', true, $3) RETURNING id::text`,
		tag+"@example.com", planID, tag+"-tok",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid)
		 VALUES ($1, 'dev', gen_random_uuid()) RETURNING xray_uuid::text`,
		userID,
	).Scan(&deviceUUID); err != nil {
		t.Fatalf("seed device: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM devices WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, planID)
		_, _ = pool.Exec(ctx, `DELETE FROM inbounds WHERE node_id = $1`, nodeID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_group_nodes WHERE node_id = $1`, nodeID)
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, groupID)
	})

	return nodeID, deviceUUID
}

func servesDevice(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID, deviceUUID string) bool {
	t.Helper()
	_, users, err := NewXrayConfigService(pool).generate(ctx, nodeID)
	if err != nil {
		t.Fatalf("generate config: %v", err)
	}
	for _, u := range users {
		if u.UUID == deviceUUID {
			return true
		}
	}
	return false
}

func openEvictionPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed config tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

// An evicted device must disappear from the generated config; that removal is
// the entire mechanism by which a concurrency cap has any effect.
func TestEvictedDeviceIsNotServedTheConfig(t *testing.T) {
	pool, ctx := openEvictionPool(t)
	nodeID, deviceUUID := seedEvictionFixture(t, ctx, pool)

	if !servesDevice(t, ctx, pool, nodeID, deviceUUID) {
		t.Fatal("device is absent before any eviction; the fixture is wrong")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE devices SET evicted_until = NOW() + INTERVAL '10 minutes' WHERE xray_uuid = $1`,
		deviceUUID,
	); err != nil {
		t.Fatalf("evict device: %v", err)
	}

	if servesDevice(t, ctx, pool, nodeID, deviceUUID) {
		t.Fatal("evicted device is still in the config; the cap would do nothing")
	}
}

// Re-admission is driven by the deadline alone. Nothing clears the column, so
// if an elapsed deadline did not restore service the user would be locked out
// permanently by a ten-minute penalty.
func TestServiceReturnsOnItsOwnWhenTheCooldownElapses(t *testing.T) {
	pool, ctx := openEvictionPool(t)
	nodeID, deviceUUID := seedEvictionFixture(t, ctx, pool)

	if _, err := pool.Exec(ctx,
		`UPDATE devices SET evicted_until = NOW() - INTERVAL '1 second' WHERE xray_uuid = $1`,
		deviceUUID,
	); err != nil {
		t.Fatalf("set elapsed deadline: %v", err)
	}

	if !servesDevice(t, ctx, pool, nodeID, deviceUUID) {
		t.Fatal("device still excluded after its cooldown elapsed; eviction is permanent")
	}

	var stillSet bool
	if err := pool.QueryRow(ctx,
		`SELECT evicted_until IS NOT NULL FROM devices WHERE xray_uuid = $1`, deviceUUID,
	).Scan(&stillSet); err != nil {
		t.Fatalf("read deadline: %v", err)
	}
	if !stillSet {
		t.Error("expected the elapsed deadline to remain on the row as an audit trail")
	}
}

// A deadline still in the future must keep the device out; a filter comparing
// the wrong way round would serve exactly the devices it is meant to withhold.
func TestFutureDeadlineKeepsTheDeviceOutAndPastOneDoesNot(t *testing.T) {
	pool, ctx := openEvictionPool(t)
	nodeID, deviceUUID := seedEvictionFixture(t, ctx, pool)

	for _, tc := range []struct {
		name     string
		deadline time.Duration
		served   bool
	}{
		{"one hour from now", time.Hour, false},
		{"one second from now", time.Second, false},
		{"one hour ago", -time.Hour, true},
	} {
		if _, err := pool.Exec(ctx,
			`UPDATE devices SET evicted_until = NOW() + $1::interval WHERE xray_uuid = $2`,
			tc.deadline.String(), deviceUUID,
		); err != nil {
			t.Fatalf("%s: set deadline: %v", tc.name, err)
		}
		if got := servesDevice(t, ctx, pool, nodeID, deviceUUID); got != tc.served {
			t.Errorf("%s: served = %v, want %v", tc.name, got, tc.served)
		}
	}
}

// Only the evicted credential loses service. Sharing one device must not cost
// a user the devices they are using legitimately.
func TestEvictionLeavesTheUsersOtherDevicesServed(t *testing.T) {
	pool, ctx := openEvictionPool(t)
	nodeID, evictedUUID := seedEvictionFixture(t, ctx, pool)

	var userID string
	if err := pool.QueryRow(ctx,
		`SELECT user_id::text FROM devices WHERE xray_uuid = $1`, evictedUUID,
	).Scan(&userID); err != nil {
		t.Fatalf("read user: %v", err)
	}

	var keptUUID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid)
		 VALUES ($1, 'kept', gen_random_uuid()) RETURNING xray_uuid::text`, userID,
	).Scan(&keptUUID); err != nil {
		t.Fatalf("seed second device: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE devices SET evicted_until = NOW() + INTERVAL '10 minutes' WHERE xray_uuid = $1`,
		evictedUUID,
	); err != nil {
		t.Fatalf("evict device: %v", err)
	}

	if servesDevice(t, ctx, pool, nodeID, evictedUUID) {
		t.Error("evicted device is still served")
	}
	if !servesDevice(t, ctx, pool, nodeID, keptUUID) {
		t.Error("evicting one device also removed the user's other device")
	}
}
