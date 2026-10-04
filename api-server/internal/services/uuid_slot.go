package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const UUIDSlotDeniedReason = "concurrency_limit"

// AccountUUIDSlotKey is shared by all Exits. Membership is deliberately not
// time-based: an idle but connected tunnel must not lose its admitted slot.
// A liveness reconciler may remove members only after a complete, fenced
// observation of all serving Exits proves that UUID has disconnected.
func AccountUUIDSlotKey(userID string) string { return "account_uuid_slots:" + userID }

// ReserveAccountUUIDSlot atomically admits this credential against the current
// plan limit. An already admitted UUID keeps its slot even after a plan
// downgrade; newcomers are denied until membership falls below the new limit.
// The caller must authorize the UUID and account in the DB before calling.
func ReserveAccountUUIDSlot(ctx context.Context, rdb *redis.Client, userID, deviceUUID string, limit int) (bool, error) {
	if rdb == nil {
		return false, errors.New("UUID slot Redis unavailable")
	}
	if userID == "" || deviceUUID == "" || limit < 0 {
		return false, errors.New("invalid UUID slot reservation")
	}
	if limit == 0 {
		return true, nil // plan convention: no concurrent UUID cap
	}
	admitted, err := reserveAccountUUIDSlotScript.Run(ctx, rdb, []string{AccountUUIDSlotKey(userID)}, deviceUUID, limit).Int64()
	if err != nil {
		return false, fmt.Errorf("reserve account UUID slot: %w", err)
	}
	if admitted != 0 && admitted != 1 {
		return false, errors.New("invalid UUID slot Redis response")
	}
	return admitted == 1, nil
}

var reserveAccountUUIDSlotScript = redis.NewScript(`
if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then
  return 1
end
if redis.call('SCARD', KEYS[1]) >= tonumber(ARGV[2]) then
  return 0
end
local clock=redis.call('TIME');local timestamp=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local nonce=redis.call('INCR',KEYS[1]..':sequence')
redis.call('HSET',KEYS[1]..':reservations',ARGV[1],nonce)
redis.call('HSET',KEYS[1]..':reserved_at',ARGV[1],timestamp)
redis.call('SADD', KEYS[1], ARGV[1])
return 1
`)
