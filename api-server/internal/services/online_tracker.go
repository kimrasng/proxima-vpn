package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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

// PublishOnlineReport replaces one Exit report; empty reports clear it immediately.
func (t *OnlineTracker) PublishOnlineReport(ctx context.Context, nodeID string, uuids []string, ips any) error {
	key := "node:" + nodeID + ":online"
	ipKey := "node:" + nodeID + ":online_ips"
	stampKey := "node:" + nodeID + ":online_report"
	if len(uuids) == 0 {
		pipe := t.redis.TxPipeline()
		pipe.Set(ctx, key, `[]`, 20*time.Second)
		pipe.Set(ctx, ipKey, `{}`, 20*time.Second)
		pipe.Set(ctx, stampKey, time.Now().Unix(), 20*time.Second)
		_, err := pipe.Exec(ctx)
		return err
	}
	data, err := json.Marshal(uuids)
	if err != nil {
		return err
	}
	ipData, err := json.Marshal(ips)
	if err != nil {
		return err
	}
	pipe := t.redis.TxPipeline()
	pipe.Set(ctx, key, data, 20*time.Second)
	pipe.Set(ctx, ipKey, ipData, 20*time.Second)
	pipe.Set(ctx, stampKey, time.Now().Unix(), 20*time.Second)
	_, err = pipe.Exec(ctx)
	return err
}

// OnlineObservation is an account-wide epoch. An incomplete epoch is never
// evidence that an absent UUID went offline. Equal starts are deliberately
// ambiguous: a sweep cannot infer the order of connections between reports.
type OnlineObservation struct {
	OnlineUUIDs    []string
	UUIDs          []string
	Starts         map[string]int64
	Complete       bool
	UnknownNodeIDs []string
	UnknownExitIDs []string
	NewestUUID     string
	AmbiguousUUIDs []string
	Ambiguous      bool
}

// OnlineUUIDsForUser retains the strict legacy contract: incomplete reports
// are errors, not a zero count.
func (t *OnlineTracker) OnlineUUIDsForUser(ctx context.Context, db *pgxpool.Pool, userID string) ([]string, error) {
	obs, err := t.ObserveOnlineForUser(ctx, db, userID, time.Now())
	if err != nil {
		return nil, err
	}
	if !obs.Complete {
		return nil, fmt.Errorf("incomplete online observation: unknown Exits %v", obs.UnknownNodeIDs)
	}
	return obs.OnlineUUIDs, nil
}

// ObserveAccount is the scheduler's single account-wide observation contract.
func (t *OnlineTracker) ObserveAccount(ctx context.Context, db *pgxpool.Pool, userID string) (OnlineObservation, error) {
	return t.ObserveOnlineForUser(ctx, db, userID, time.Now())
}

// ObserveOnlineForUser reads every serving Exit (including stale nodes) and
// commits transitions only after a complete account-wide observation.
func (t *OnlineTracker) ObserveOnlineForUser(ctx context.Context, db *pgxpool.Pool, userID string, now time.Time) (OnlineObservation, error) {
	rows, err := db.Query(ctx, `SELECT xray_uuid::text FROM devices WHERE user_id = $1`, userID)
	if err != nil {
		return OnlineObservation{}, err
	}
	defer rows.Close()
	owned := map[string]struct{}{}
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return OnlineObservation{}, err
		}
		owned[uuid] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return OnlineObservation{}, err
	}
	nodes, err := t.servingExitIDs(ctx, db)
	if err != nil {
		return OnlineObservation{}, err
	}
	obs := OnlineObservation{Complete: true, Starts: map[string]int64{}}
	online := map[string]struct{}{}
	for node, fresh := range nodes {
		stamp, err := t.redis.Get(ctx, "node:"+node+":online_report").Int64()
		if err != nil && err != redis.Nil {
			return OnlineObservation{}, err
		}
		if !fresh || err == redis.Nil || stamp < now.Unix()-20 || stamp > now.Unix() {
			obs.Complete = false
			obs.UnknownNodeIDs = append(obs.UnknownNodeIDs, node)
			continue
		}
		uuids, err := t.GetOnlineUsers(ctx, node)
		if err != nil {
			obs.Complete = false
			obs.UnknownNodeIDs = append(obs.UnknownNodeIDs, node)
			continue
		}
		if uuids == nil {
			obs.Complete = false
			obs.UnknownNodeIDs = append(obs.UnknownNodeIDs, node)
			continue
		}
		if len(uuids) == 0 {
			continue
		} // explicit empty report
		data, err := t.redis.Get(ctx, "node:"+node+":online_ips").Bytes()
		if err == redis.Nil {
			obs.Complete = false
			obs.UnknownNodeIDs = append(obs.UnknownNodeIDs, node)
			continue
		}
		if err != nil {
			return OnlineObservation{}, err
		}
		var byUUID map[string][]struct {
			IP       string `json:"ip"`
			LastSeen int64  `json:"last_seen"`
		}
		if err := json.Unmarshal(data, &byUUID); err != nil || byUUID == nil {
			obs.Complete = false
			obs.UnknownNodeIDs = append(obs.UnknownNodeIDs, node)
			continue
		}
		for _, uuid := range uuids {
			if _, ok := owned[uuid]; !ok {
				continue
			}
			for _, entry := range byUUID[uuid] {
				if entry.LastSeen >= now.Unix()-20 && entry.LastSeen <= now.Unix() {
					online[uuid] = struct{}{}
					break
				}
			}
		}
	}
	for uuid := range online {
		obs.OnlineUUIDs = append(obs.OnlineUUIDs, uuid)
	}
	sort.Strings(obs.OnlineUUIDs)
	obs.UUIDs = obs.OnlineUUIDs
	sort.Strings(obs.UnknownNodeIDs)
	obs.UnknownExitIDs = obs.UnknownNodeIDs
	if !obs.Complete {
		return obs, nil
	}
	if err := t.ObserveSessionStarts(ctx, userID, obs.OnlineUUIDs, now); err != nil {
		return OnlineObservation{}, err
	}
	obs.Starts, err = t.GetSessionStarts(ctx, obs.OnlineUUIDs)
	if err != nil {
		return OnlineObservation{}, err
	}
	var latest int64
	for _, uuid := range obs.OnlineUUIDs {
		start, known := obs.Starts[uuid]
		if !known {
			obs.AmbiguousUUIDs = append(obs.AmbiguousUUIDs, uuid)
			continue
		}
		if start > latest {
			latest = start
			obs.AmbiguousUUIDs = []string{uuid}
		} else if start == latest {
			obs.AmbiguousUUIDs = append(obs.AmbiguousUUIDs, uuid)
		}
	}
	if len(obs.AmbiguousUUIDs) == 1 && len(obs.Starts) == len(obs.OnlineUUIDs) {
		obs.NewestUUID = obs.AmbiguousUUIDs[0]
		obs.AmbiguousUUIDs = nil
	} else if len(obs.OnlineUUIDs) > 0 {
		obs.Ambiguous = true
	}
	return obs, nil
}

// ObserveSessionStarts preserves starts while a UUID remains in the account-wide
// union, even when it moves between Exits. Call after every scheduler sweep.
// The prior account set detects a confirmed offline transition; starts are
// re-created only if a UUID returns after disappearing from that set.
func (t *OnlineTracker) ObserveSessionStarts(ctx context.Context, userID string, online []string, now time.Time) error {
	// One Lua transaction serializes concurrent sweeps. Never expire the prior
	// set or starts: a missed sweep is not proof of an offline transition.
	const script = `
local previous = redis.call('SMEMBERS', KEYS[1])
local current = {}
for i = 1, #ARGV - 1 do current[ARGV[i]] = true end
for _, uuid in ipairs(previous) do
  if not current[uuid] then redis.call('DEL', 'device:' .. uuid .. ':online_since') end
end
redis.call('DEL', KEYS[1])
for i = 1, #ARGV - 1 do
  local uuid = ARGV[i]
  redis.call('SADD', KEYS[1], uuid)
  redis.call('SETNX', 'device:' .. uuid .. ':online_since', ARGV[#ARGV])
end
return 1`
	args := make([]interface{}, 0, len(online)+1)
	seen := map[string]bool{}
	for _, uuid := range online {
		if !seen[uuid] {
			args = append(args, uuid)
			seen[uuid] = true
		}
	}
	args = append(args, now.Unix())
	return t.redis.Eval(ctx, script, []string{"account:" + userID + ":online_uuids"}, args...).Err()
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
//
// Reads the per-IP report rather than the older uuid-list key: that list is
// derived from traffic counters which are read destructively, so it arrives
// empty and every caller saw nobody online. Falls back to the old key for an
// agent too old to send addresses.
func (t *OnlineTracker) GetAllOnlineUUIDs(ctx context.Context) (map[string]string, error) {
	result := make(map[string]string)

	ipKeys, err := t.scanKeys(ctx, "node:*:online_ips")
	if err != nil {
		return nil, err
	}
	for _, key := range ipKeys {
		nodeID := nodeIDFromKey(key)
		if nodeID == "" {
			continue
		}
		data, err := t.redis.Get(ctx, key).Bytes()
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
			if len(ips) > 0 {
				result[uuid] = nodeID
			}
		}
	}

	keys, err := t.scanKeys(ctx, "node:*:online")
	if err != nil {
		return result, nil
	}
	for _, key := range keys {
		nodeID := nodeIDFromKey(key)
		if nodeID == "" {
			continue
		}
		data, err := t.redis.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var uuids []string
		if err := json.Unmarshal(data, &uuids); err != nil {
			continue
		}
		for _, u := range uuids {
			if _, better := result[u]; !better {
				result[u] = nodeID
			}
		}
	}
	return result, nil
}

func nodeIDFromKey(key string) string {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

// CountOnlineForUser counts the user's credentials that are online anywhere in
// the node pool. Counted per user rather than per node: a cap that reset on
// every node would let one account multiply itself by the pool size. UUIDs are
// deduplicated because a device that roams between nodes can appear in two
// node sets until the stale one expires.
func (t *OnlineTracker) CountOnlineForUser(ctx context.Context, db *pgxpool.Pool, userID string) (int, error) {
	uuids, err := t.OnlineUUIDsForUser(ctx, db, userID)
	return len(uuids), err
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

// ForgetDevice drops one device from the cached online reports so a terminated
// session stops being listed before its TTL lapses. Best-effort: the next agent
// report is the authority, so a failure here only leaves a stale row behind.
//
// The remaining entries are rewritten with the key's own remaining TTL rather
// than a fresh one, so removing a device cannot extend how long a dead node's
// report is trusted.
func (t *OnlineTracker) ForgetDevice(ctx context.Context, xrayUUID string) {
	if xrayUUID == "" {
		return
	}

	t.redis.Del(ctx, "device:"+xrayUUID+":online_since")

	ipKeys, err := t.scanKeys(ctx, "node:*:online_ips")
	if err == nil {
		for _, key := range ipKeys {
			data, err := t.redis.Get(ctx, key).Bytes()
			if err != nil {
				continue
			}
			var byUUID map[string]json.RawMessage
			if err := json.Unmarshal(data, &byUUID); err != nil {
				continue
			}
			if _, present := byUUID[xrayUUID]; !present {
				continue
			}
			delete(byUUID, xrayUUID)
			t.rewrite(ctx, key, byUUID)
		}
	}

	keys, err := t.scanKeys(ctx, "node:*:online")
	if err != nil {
		return
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
		kept := make([]string, 0, len(uuids))
		for _, u := range uuids {
			if u != xrayUUID {
				kept = append(kept, u)
			}
		}
		if len(kept) == len(uuids) {
			continue
		}
		t.rewrite(ctx, key, kept)
	}
}

func (t *OnlineTracker) rewrite(ctx context.Context, key string, value any) {
	ttl, err := t.redis.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	t.redis.Set(ctx, key, encoded, ttl)
}

func (t *OnlineTracker) servingExitIDs(ctx context.Context, db *pgxpool.Pool) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT id::text, last_seen > NOW() - INTERVAL '30 seconds' FROM nodes WHERE role IN ('exit', 'both') AND status != 'pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		var fresh bool
		if err := rows.Scan(&id, &fresh); err != nil {
			return nil, err
		}
		out[id] = fresh
	}
	return out, rows.Err()
}

func (t *OnlineTracker) freshNodeIDs(ctx context.Context, db *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := db.Query(ctx,
		`SELECT id::text FROM nodes WHERE role IN ('exit', 'both') AND last_seen > NOW() - INTERVAL '30 seconds'`)
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

// GetSessionStarts returns the unix second each device was first seen online in
// its current session. UUIDs without a stamp are omitted rather than defaulted
// to now, which would report an unknown session age as a brand new connection.
func (t *OnlineTracker) GetSessionStarts(ctx context.Context, uuids []string) (map[string]int64, error) {
	starts := make(map[string]int64, len(uuids))
	if len(uuids) == 0 {
		return starts, nil
	}

	keys := make([]string, 0, len(uuids))
	for _, uuid := range uuids {
		keys = append(keys, "device:"+uuid+":online_since")
	}

	values, err := t.redis.MGet(ctx, keys...).Result()
	if err != nil {
		return starts, err
	}

	for i, raw := range values {
		text, ok := raw.(string)
		if !ok {
			continue
		}
		seconds, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			continue
		}
		starts[uuids[i]] = seconds
	}

	return starts, nil
}
