package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ReconcileAbsentTunnelSessions requires fresh independent complete fleet
// snapshots separated by 30 seconds, with a pre-observation reservation
// sequence fence. Never call it on partial reports or assume a stale session
// disappeared. This primitive is not wired to production admission yet.
func ReconcileAbsentTunnelSessions(ctx context.Context, rdb *redis.Client, account string, observation TunnelSessionObservation, snapshotID string, observedAt int64, fence map[string]string) ([]string, error) {
	if rdb == nil || observedAt <= 0 {
		return nil, fmt.Errorf("invalid session reconciliation")
	}
	user, err := uuid.Parse(account)
	if err != nil {
		return nil, err
	}
	if observation.Complete && fence == nil {
		return nil, fmt.Errorf("missing pre-observation reservation fence")
	}
	keys := []string{}
	if observation.Complete {
		if snapshotID == "" {
			return nil, fmt.Errorf("missing fleet snapshot identity")
		}
		seen := map[string]bool{}
		for _, s := range observation.Sessions {
			key, err := TunnelSessionKey(s)
			if err != nil {
				return nil, err
			}
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	before, err := json.Marshal(fence)
	if err != nil {
		return nil, err
	}
	const script = `
local epoch=tonumber(ARGV[1])
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
if epoch>now or epoch<now-25000 then redis.call('DEL',KEYS[3]);return redis.error_reply('stale session epoch') end
local previous=tonumber(redis.call('GET',KEYS[4]) or '0')
if ARGV[2]~='1' or ARGV[5]=='' then
 redis.call('DEL',KEYS[3])
 if epoch>previous then redis.call('SET',KEYS[4],epoch) end
 return {}
end
if epoch<=previous then return {} end
redis.call('SET',KEYS[4],epoch)
local current={}
for _,session in ipairs(cjson.decode(ARGV[3])) do current[session]=true end
local fence=cjson.decode(ARGV[4])
local released={}
for _,session in ipairs(redis.call('SMEMBERS',KEYS[1])) do
 local sequence=redis.call('HGET',KEYS[2],session)
 if current[session] or not sequence or fence[session]~=sequence then
  redis.call('HDEL',KEYS[3],session)
 else
  local raw=redis.call('HGET',KEYS[3],session)
  local absence=raw and cjson.decode(raw) or nil
  if absence and absence.sequence==sequence and absence.snapshot~=ARGV[5] and epoch-absence.since>=30000 then
   redis.call('SREM',KEYS[1],session)
   redis.call('HDEL',KEYS[3],session)
   table.insert(released,session)
  elseif not absence or absence.sequence~=sequence or not absence.snapshot then
   redis.call('HSET',KEYS[3],session,cjson.encode({sequence=sequence,since=epoch,snapshot=ARGV[5]}))
  end
 end
end
return released`
	complete := "0"
	if observation.Complete {
		complete = "1"
	}
	key := TunnelSlotKey(user.String())
	result, err := redis.NewScript(script).Run(ctx, rdb, []string{key, key + ":sequence", key + ":absence", key + ":epoch"}, strconv.FormatInt(observedAt, 10), complete, data, before, snapshotID).StringSlice()
	if result == nil && err == nil {
		result = []string{}
	}
	return result, err
}

func CaptureTunnelSessionFence(ctx context.Context, rdb *redis.Client, account string) (map[string]string, error) {
	if rdb == nil {
		return nil, fmt.Errorf("session fence storage unavailable")
	}
	user, err := uuid.Parse(account)
	if err != nil {
		return nil, err
	}
	return rdb.HGetAll(ctx, TunnelSlotKey(user.String())+":sequence").Result()
}

func ObserveAndReconcileTunnelSessions(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, account string) (TunnelSessionObservation, []string, error) {
	if db == nil || rdb == nil {
		return TunnelSessionObservation{}, nil, fmt.Errorf("session reconciliation unavailable")
	}
	clock, err := rdb.Time(ctx).Result()
	if err != nil {
		return TunnelSessionObservation{}, nil, err
	}
	fence, err := CaptureTunnelSessionFence(ctx, rdb, account)
	if err != nil {
		return TunnelSessionObservation{}, nil, err
	}
	obs, snapshot, err := ObserveAccountTunnelSessions(ctx, db, rdb, account)
	if err != nil {
		_, _ = ReconcileAbsentTunnelSessions(ctx, rdb, account, TunnelSessionObservation{Complete: false}, "", clock.UnixMilli(), fence)
		return obs, nil, err
	}
	released, err := ReconcileAbsentTunnelSessions(ctx, rdb, account, obs, snapshot, clock.UnixMilli(), fence)
	return obs, released, err
}
