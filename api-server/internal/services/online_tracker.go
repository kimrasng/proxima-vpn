package services

import (
	"context"
	"encoding/json"
	"strings"

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
