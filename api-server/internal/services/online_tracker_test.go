package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type onlineIPFixture struct {
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
}

func setupTracker(t *testing.T) (*pgxpool.Pool, *redis.Client, *OnlineTracker, context.Context) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed tracker tests")
	}
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("TEST_REDIS_ADDR not set; skipping Redis-backed tracker tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unreachable at %s: %v", redisAddr, err)
	}

	return pool, rdb, NewOnlineTracker(rdb), ctx
}

// seedUserWithDevices creates a plan, a user and its devices, returning the
// user id and the device UUIDs.
func seedUserWithDevices(t *testing.T, ctx context.Context, pool *pgxpool.Pool, count int) (string, []string) {
	t.Helper()

	var groupID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`,
		fmt.Sprintf("cap-test-group-%d", os.Getpid()),
	).Scan(&groupID); err != nil {
		t.Fatalf("seed node group: %v", err)
	}

	var planID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		 VALUES ($1, 30, 5, $2) RETURNING id::text`,
		fmt.Sprintf("cap-test-plan-%d", os.Getpid()), groupID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, plan_id, status, is_active, sub_token)
		 VALUES ($1, 'x', $2, 'active', true, $3) RETURNING id::text`,
		fmt.Sprintf("cap-test-%d@example.com", os.Getpid()), planID,
		fmt.Sprintf("cap-tok-%d", os.Getpid()),
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	uuids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		var uuid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO devices (user_id, name, xray_uuid)
			 VALUES ($1, $2, gen_random_uuid()) RETURNING xray_uuid::text`,
			userID, fmt.Sprintf("dev-%d", i),
		).Scan(&uuid); err != nil {
			t.Fatalf("seed device: %v", err)
		}
		uuids = append(uuids, uuid)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM devices WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, planID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, groupID)
	})

	return userID, uuids
}

// seedNode registers a node whose agent either reported just now or long ago.
func seedNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, stale bool) string {
	t.Helper()

	lastSeen := "NOW()"
	if stale {
		lastSeen = "NOW() - INTERVAL '30 minutes'"
	}

	var nodeID string
	if err := pool.QueryRow(ctx, fmt.Sprintf(
		`INSERT INTO nodes (name, ip, api_key, status, last_seen)
		 VALUES ($1, '198.51.100.1', $2, 'online', %s) RETURNING id::text`, lastSeen),
		name, fmt.Sprintf("key-%s-%d", name, os.Getpid()),
	).Scan(&nodeID); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID)
	})
	return nodeID
}

func publishOnlineIPs(t *testing.T, ctx context.Context, rdb *redis.Client, nodeID string, byUUID map[string][]onlineIPFixture) {
	t.Helper()

	data, err := json.Marshal(byUUID)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	key := "node:" + nodeID + ":online_ips"
	if err := rdb.Set(ctx, key, string(data), 0).Err(); err != nil {
		t.Fatalf("publish online ips: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })
}

// The cap covers the user's whole node pool. Counting per node would let one
// account open its full allowance again on every node.
func TestDistinctIPsAreSummedAcrossTheWholePool(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	userID, uuids := seedUserWithDevices(t, ctx, pool, 2)

	nodeA := seedNode(t, ctx, pool, "cap-node-a", false)
	nodeB := seedNode(t, ctx, pool, "cap-node-b", false)

	publishOnlineIPs(t, ctx, rdb, nodeA, map[string][]onlineIPFixture{
		uuids[0]: {{IP: "203.0.113.1", LastSeen: 100}},
	})
	publishOnlineIPs(t, ctx, rdb, nodeB, map[string][]onlineIPFixture{
		uuids[1]: {{IP: "203.0.113.2", LastSeen: 200}},
	})

	total, perDevice, err := tracker.CountDistinctIPsForUser(ctx, pool, userID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Fatalf("counted %d addresses across two nodes, want 2 (per-node counting would give 1)", total)
	}
	if len(perDevice) != 2 {
		t.Fatalf("expected both devices in the breakdown, got %v", perDevice)
	}
}

// A device that roams between nodes lingers in the old node's set until it
// expires; counting it twice would push a single device over a cap of 1.
func TestSameAddressOnTwoNodesCountsOnce(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	userID, uuids := seedUserWithDevices(t, ctx, pool, 1)

	nodeA := seedNode(t, ctx, pool, "cap-roam-a", false)
	nodeB := seedNode(t, ctx, pool, "cap-roam-b", false)

	same := []onlineIPFixture{{IP: "203.0.113.50", LastSeen: 100}}
	publishOnlineIPs(t, ctx, rdb, nodeA, map[string][]onlineIPFixture{uuids[0]: same})
	publishOnlineIPs(t, ctx, rdb, nodeB, map[string][]onlineIPFixture{uuids[0]: same})

	total, _, err := tracker.CountDistinctIPsForUser(ctx, pool, userID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Fatalf("counted %d for one roaming device, want 1", total)
	}
}

// A node whose agent stopped reporting keeps its Redis key until the TTL
// lapses. Trusting it bills a user for connections that no longer exist.
func TestStaleNodesDoNotInflateTheCount(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	userID, uuids := seedUserWithDevices(t, ctx, pool, 1)

	live := seedNode(t, ctx, pool, "cap-live", false)
	dead := seedNode(t, ctx, pool, "cap-dead", true)

	publishOnlineIPs(t, ctx, rdb, live, map[string][]onlineIPFixture{
		uuids[0]: {{IP: "203.0.113.1", LastSeen: 100}},
	})
	publishOnlineIPs(t, ctx, rdb, dead, map[string][]onlineIPFixture{
		uuids[0]: {{IP: "203.0.113.99", LastSeen: 100}},
	})

	total, _, err := tracker.CountDistinctIPsForUser(ctx, pool, userID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Fatalf("counted %d; the dead node's stale entry must not count", total)
	}
}

// Another account's traffic must never land on this user's tally.
func TestOtherUsersDevicesAreNotCounted(t *testing.T) {
	pool, rdb, tracker, ctx := setupTracker(t)
	userID, uuids := seedUserWithDevices(t, ctx, pool, 1)
	node := seedNode(t, ctx, pool, "cap-foreign", false)

	publishOnlineIPs(t, ctx, rdb, node, map[string][]onlineIPFixture{
		uuids[0]:                               {{IP: "203.0.113.1", LastSeen: 100}},
		"11111111-1111-1111-1111-111111111111": {{IP: "203.0.113.7", LastSeen: 100}},
	})

	total, perDevice, err := tracker.CountDistinctIPsForUser(ctx, pool, userID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Fatalf("counted %d, want 1; a foreign UUID leaked in", total)
	}
	if _, leaked := perDevice["11111111-1111-1111-1111-111111111111"]; leaked {
		t.Fatal("foreign device appeared in the breakdown")
	}
}

// An agent too old to report addresses leaves the key absent entirely, which
// must read as "nothing known" rather than erroring the dashboard.
func TestMissingOnlineIPKeyReadsAsZero(t *testing.T) {
	pool, _, tracker, ctx := setupTracker(t)
	userID, _ := seedUserWithDevices(t, ctx, pool, 1)
	seedNode(t, ctx, pool, "cap-silent", false)

	total, perDevice, err := tracker.CountDistinctIPsForUser(ctx, pool, userID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 0 || len(perDevice) != 0 {
		t.Fatalf("got %d/%v with no agent report, want 0/empty", total, perDevice)
	}
}
