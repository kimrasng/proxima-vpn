package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// AddressReservationFence must be captured BEFORE collecting a complete fleet
// observation. Any admission after capture changes the per-address sequence
// and prevents that observation from releasing its reservation.
type AddressReservationFence map[string]string

func CaptureAddressReservationFence(ctx context.Context, rdb *redis.Client, account string) (AddressReservationFence, error) {
	if rdb == nil || account == "" {
		return nil, fmt.Errorf("address reservation storage unavailable")
	}
	values, err := rdb.HGetAll(ctx, AddressSlotKey(account)+":sequence").Result()
	return AddressReservationFence(values), err
}

// ReconcileAbsentClientAddresses releases only after two distinct complete
// fleet observations separated by at least 30 seconds. observedAt is the
// server timestamp captured before collecting reports, not a client timestamp.
// Reusing the same report fingerprint cannot supply a second observation.
// Incomplete or unidentified observations invalidate absence evidence. This
// is not wired to admission yet.
func ReconcileAbsentClientAddresses(ctx context.Context, rdb *redis.Client, account string, observation ClientAddressObservation, observedAt int64, fence AddressReservationFence) ([]string, error) {
	if rdb == nil || account == "" || observedAt <= 0 {
		return nil, fmt.Errorf("invalid address reconciliation")
	}
	if observation.Complete && fence == nil {
		return nil, fmt.Errorf("missing pre-observation reservation fence")
	}
	addresses := make([]string, 0, len(observation.Addresses))
	for _, raw := range observation.Addresses {
		// Same slot key as admission, so a live /64 keeps the slot it holds.
		slot, err := NormalizeClientAddress(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid observed address")
		}
		addresses = append(addresses, slot)
	}
	sort.Strings(addresses)
	live, err := json.Marshal(addresses)
	if err != nil {
		return nil, err
	}
	sequences, err := json.Marshal(fence)
	if err != nil {
		return nil, err
	}
	const script = `
local epoch=tonumber(ARGV[1])
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
if epoch>now or epoch<now-25000 then
 redis.call('DEL',KEYS[3])
 return redis.error_reply('stale reconciliation epoch')
end
local previous=tonumber(redis.call('GET',KEYS[4]) or '0')
if ARGV[2]~='1' or ARGV[5]=='' then
 redis.call('DEL',KEYS[3])
 if epoch>previous then redis.call('SET',KEYS[4],epoch) end
 return {}
end
if epoch<=previous then return {} end
redis.call('SET',KEYS[4],epoch)
local current={}
for _,address in ipairs(cjson.decode(ARGV[3])) do current[address]=true end
local fence=cjson.decode(ARGV[4])
local released={}
for _,address in ipairs(redis.call('SMEMBERS',KEYS[1])) do
 local sequence=redis.call('HGET',KEYS[2],address)
 if current[address] or not sequence or fence[address]~=sequence then
  redis.call('HDEL',KEYS[3],address)
 else
  local raw=redis.call('HGET',KEYS[3],address)
  local absence=raw and cjson.decode(raw) or nil
  if absence and absence.sequence==sequence and absence.snapshot and absence.snapshot~=ARGV[5] and epoch-absence.since>=30000 then
   redis.call('SREM',KEYS[1],address)
   redis.call('HDEL',KEYS[3],address)
   table.insert(released,address)
  elseif not absence or absence.sequence~=sequence or not absence.snapshot then
   redis.call('HSET',KEYS[3],address,cjson.encode({sequence=sequence,since=epoch,snapshot=ARGV[5]}))
  end
 end
end
return released`
	complete := "0"
	if observation.Complete {
		complete = "1"
	}
	key := AddressSlotKey(account)
	result, err := redis.NewScript(script).Run(ctx, rdb, []string{key, key + ":sequence", key + ":absence", key + ":epoch"}, strconv.FormatInt(observedAt, 10), complete, live, sequences, observation.SnapshotID).StringSlice()
	if result == nil && err == nil {
		result = []string{}
	}
	return result, err
}
