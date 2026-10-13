package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/redis/go-redis/v9"
)

// PreserveClientAddressStarts records account-wide address transitions only
// after a complete observation. A source's extra connections or Exit switches
// must not reset its age and make a long-running client the newest target.
func PreserveClientAddressStarts(ctx context.Context, rdb *redis.Client, userID string, observation ClientAddressObservation) (ClientAddressObservation, error) {
	if !observation.Complete {
		return observation, nil
	}
	if rdb == nil {
		return observation, fmt.Errorf("client address transition storage unavailable")
	}
	const script = `
local previous = redis.call('SMEMBERS', KEYS[1])
local current = {}
for i=1,#ARGV,2 do current[ARGV[i]]=true end
for _,address in ipairs(previous) do
 if not current[address] then redis.call('HDEL',KEYS[2],address) end
end
redis.call('DEL',KEYS[1])
for i=1,#ARGV,2 do
 redis.call('SADD',KEYS[1],ARGV[i])
 redis.call('HSETNX',KEYS[2],ARGV[i],ARGV[i+1])
end
return redis.call('HGETALL',KEYS[2])`
	addresses := append([]string(nil), observation.Addresses...)
	sort.Strings(addresses)
	args := make([]any, 0, len(addresses)*2)
	for _, address := range addresses {
		start := observation.Starts[address]
		if start <= 0 {
			return observation, fmt.Errorf("missing authenticated address start")
		}
		args = append(args, address, start)
	}
	value, err := redis.NewScript(script).Run(ctx, rdb, []string{"account:" + userID + ":client_addresses", "account:" + userID + ":client_address_starts"}, args...).StringSlice()
	if err != nil {
		return observation, err
	}
	starts := map[string]int64{}
	for n := 0; n+1 < len(value); n += 2 {
		var start int64
		if err := json.Unmarshal([]byte(value[n+1]), &start); err != nil {
			return observation, err
		}
		starts[value[n]] = start
	}
	observation.Starts = starts
	return observation, nil
}
