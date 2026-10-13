package services

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/redis/go-redis/v9"
)

// ReserveObservedClientAddress atomically admits one server-observed source
// across Exits. It does not accept a client identity header. A live address
// holds one slot regardless of connection count. Reservations never expire on
// missing reports; confirmed absence must release them explicitly. This
// primitive is not wired into production admission yet.
func ReserveObservedClientAddress(ctx context.Context, rdb *redis.Client, account, address string, limit int) (bool, error) {
	if rdb == nil || account == "" {
		return false, fmt.Errorf("address admission unavailable")
	}
	ip, err := netip.ParseAddr(address)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
		return false, fmt.Errorf("invalid observed client address")
	}
	const script = `
local address=ARGV[1]
local cap=tonumber(ARGV[2])
if redis.call('SISMEMBER',KEYS[1],address)==1 then return 1 end
if cap>0 and redis.call('SCARD',KEYS[1])>=cap then return 0 end
redis.call('SADD',KEYS[1],address)
return 1`
	result, err := redis.NewScript(script).Run(ctx, rdb, []string{"account:" + account + ":observed_address_slots"}, ip.Unmap().String(), limit).Int()
	return result == 1, err
}
