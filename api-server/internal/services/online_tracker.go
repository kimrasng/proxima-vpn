package services

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// OnlineTracker queries Redis for user online status reported by Node Agents.
type OnlineTracker struct {
	redis *redis.Client
}

// NewOnlineTracker creates a new OnlineTracker.
func NewOnlineTracker(rdb *redis.Client) *OnlineTracker {
	return &OnlineTracker{redis: rdb}
}

// GetOnlineUsers returns the list of online xray UUIDs for a specific node.
func (t *OnlineTracker) GetOnlineUsers(ctx context.Context, nodeID string) ([]string, error) {
	data, err := t.redis.Get(ctx, "node:"+nodeID+":online").Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var uuids []string
	if err := json.Unmarshal(data, &uuids); err != nil {
		return nil, err
	}
	return uuids, nil
}

// GetAllOnlineCount returns the total number of unique online users across all nodes.
func (t *OnlineTracker) GetAllOnlineCount(ctx context.Context) (int, error) {
	keys, err := t.scanKeys(ctx, "node:*:online")
	if err != nil {
		return 0, err
	}

	unique := make(map[string]struct{})
	for _, key := range keys {
		data, err := t.redis.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var uuids []string
		if err := json.Unmarshal(data, &uuids); err != nil {
			continue
		}
		for _, u := range uuids {
			unique[u] = struct{}{}
		}
	}
	return len(unique), nil
}

// IsDeviceOnline checks if a specific xray UUID is online on any node.
func (t *OnlineTracker) IsDeviceOnline(ctx context.Context, xrayUUID string) (bool, error) {
	keys, err := t.scanKeys(ctx, "node:*:online")
	if err != nil {
		return false, err
	}

	for _, key := range keys {
		data, err := t.redis.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var uuids []string
		if err := json.Unmarshal(data, &uuids); err != nil {
			continue
		}
		for _, u := range uuids {
			if u == xrayUUID {
				return true, nil
			}
		}
	}
	return false, nil
}

// GetAllOnlineUUIDs returns a map of xray UUID -> node ID for all currently online users.
func (t *OnlineTracker) GetAllOnlineUUIDs(ctx context.Context) (map[string]string, error) {
	keys, err := t.scanKeys(ctx, "node:*:online")
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, key := range keys {
		parts := strings.SplitN(key, ":", 3)
		if len(parts) < 3 {
			continue
		}
		nodeID := parts[1]

		data, err := t.redis.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var uuids []string
		if err := json.Unmarshal(data, &uuids); err != nil {
			continue
		}
		for _, u := range uuids {
			result[u] = nodeID
		}
	}
	return result, nil
}

// CountOnlineForUser counts the user's credentials that are online anywhere in
// the node pool. Counted per user rather than per node: a cap that reset on
// every node would let one account multiply itself by the pool size. UUIDs are
// deduplicated because a device that roams between nodes can appear in two
// node sets until the stale one expires.
func (t *OnlineTracker) CountOnlineForUser(ctx context.Context, db *pgxpool.Pool, userID string) (int, error) {
	rows, err := db.Query(ctx, `SELECT xray_uuid FROM devices WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	owned := make(map[string]struct{})
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return 0, err
		}
		owned[uuid] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(owned) == 0 {
		return 0, nil
	}

	onlineByUUID, err := t.GetAllOnlineUUIDs(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	for uuid := range onlineByUUID {
		if _, mine := owned[uuid]; mine {
			count++
		}
	}
	return count, nil
}

// CountDistinctIPsForUser returns how many distinct source addresses are live
// across all of the user's devices, pool-wide, plus the per-device breakdown.
//
// Only nodes whose agent reported recently are counted: a node that died holds
// its Redis key until the TTL lapses, and trusting it would charge a user for
// connections that no longer exist. Distinct IPs rather than device count is
// what detects one credential shared across machines - counting devices would
// just re-measure max_devices.
func (t *OnlineTracker) CountDistinctIPsForUser(
	ctx context.Context,
	db *pgxpool.Pool,
	userID string,
) (total int, perDevice map[string]map[string]int64, err error) {
	rows, err := db.Query(ctx, `SELECT xray_uuid FROM devices WHERE user_id = $1`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	owned := make(map[string]struct{})
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return 0, nil, err
		}
		owned[uuid] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	if len(owned) == 0 {
		return 0, map[string]map[string]int64{}, nil
	}

	fresh, err := t.freshNodeIDs(ctx, db)
	if err != nil {
		return 0, nil, err
	}

	perDevice = map[string]map[string]int64{}
	distinct := make(map[string]struct{})

	for nodeID := range fresh {
		data, err := t.redis.Get(ctx, "node:"+nodeID+":online_ips").Bytes()
		if err != nil {
			continue
		}
		var byUUID map[string][]struct {
			IP       string `json:"ip"`
			LastSeen int64  `json:"last_seen"`
		}
		if err := json.Unmarshal(data, &byUUID); err != nil {
			continue
		}
		for uuid, ips := range byUUID {
			if _, mine := owned[uuid]; !mine {
				continue
			}
			if perDevice[uuid] == nil {
				perDevice[uuid] = map[string]int64{}
			}
			for _, entry := range ips {
				distinct[entry.IP] = struct{}{}
				if entry.LastSeen > perDevice[uuid][entry.IP] {
					perDevice[uuid][entry.IP] = entry.LastSeen
				}
			}
		}
	}

	return len(distinct), perDevice, nil
}

func (t *OnlineTracker) freshNodeIDs(ctx context.Context, db *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := db.Query(ctx,
		`SELECT id::text FROM nodes WHERE last_seen > NOW() - INTERVAL '2 minutes'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

func (t *OnlineTracker) scanKeys(ctx context.Context, pattern string) ([]string, error) {
	var keys []string
	iter := t.redis.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
