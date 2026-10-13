package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

// TunnelSessionKey identifies one authenticated Exit transport generation.
// The canonical algorithm lives in nodeprov so the control plane and node-agent
// derive the identical opaque credential from the same observation.
func TunnelSessionKey(session ObservedTunnelSession) (string, error) {
	return nodeprov.TunnelSessionToken(session.ExitID, session.Connection)
}

func TunnelSlotKey(account string) string { return "account:" + account + ":tunnel_session_slots" }

// ReserveTunnelSession atomically reserves one exact transport slot. The
// operation is idempotent for an existing token and does not assign a TTL.
func ReserveTunnelSession(ctx context.Context, rdb *redis.Client, account string, session ObservedTunnelSession, limit int) (bool, error) {
	if rdb == nil {
		return false, fmt.Errorf("session admission unavailable")
	}
	user, err := uuid.Parse(account)
	if err != nil || limit < 0 {
		return false, fmt.Errorf("invalid session admission")
	}
	key, err := TunnelSessionKey(session)
	if err != nil {
		return false, err
	}
	const script = `
local key=ARGV[1]
if redis.call('SISMEMBER',KEYS[1],key)==1 then
 redis.call('HDEL',KEYS[3],key)
 return 1
end
local cap=tonumber(ARGV[2])
if cap>0 and redis.call('SCARD',KEYS[1])>=cap then return 0 end
redis.call('SADD',KEYS[1],key)
redis.call('HINCRBY',KEYS[2],key,1)
redis.call('HDEL',KEYS[3],key)
return 1`
	prefix := TunnelSlotKey(user.String())
	result, err := redis.NewScript(script).Run(ctx, rdb, []string{prefix, prefix + ":sequence", prefix + ":absence"}, key, limit).Int()
	return result == 1, err
}
