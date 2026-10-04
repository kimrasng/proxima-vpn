package services

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ReconcileAccountUUIDSlots never interprets a missing Exit report as a
// disconnect. The first complete absence starts a candidate window; a second
// complete observation after >2 report intervals may release the member, but
// only when its Redis reservation sequence has not changed since the first.
// A new admission after the first observation changes that sequence and fences
// the release even if its node has not yet reported the association.
func ReconcileAccountUUIDSlots(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, userID string) ([]string, error) {
	if db == nil || rdb == nil {
		return nil, fmt.Errorf("UUID slot reconciliation unavailable")
	}
	rows, err := db.Query(ctx, `SELECT id::text,last_seen>NOW()-INTERVAL '20 seconds' FROM nodes WHERE role IN ('exit','both') AND status!='pending' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []string{}
	for rows.Next() {
		var node string
		var fresh bool
		if err := rows.Scan(&node, &fresh); err != nil {
			return nil, err
		}
		if !fresh {
			return nil, fmt.Errorf("incomplete Exit: %s", node)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no Exit reports")
	}
	key := AccountUUIDSlotKey(userID)
	keys := []string{key}
	args := make([]interface{}, 0, len(nodes))
	for _, node := range nodes {
		keys = append(keys, "node:"+node+":admitted")
		generation, err := rdb.HGet(ctx, "node:"+node+":admitted:fence", "generation").Result()
		if err != nil || generation == "" {
			return nil, fmt.Errorf("missing Exit generation: %s", node)
		}
		keys = append(keys, "node:"+node+":admitted:fence")
		args = append(args, generation)
	}
	script := redis.NewScript(`
 local now=redis.call('TIME');local ms=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
 local live={}
 local minPublished=ms
 for i=2,#KEYS,2 do
   if redis.call('PTTL',KEYS[i])<15000 then return redis.error_reply('missing or stale Exit association report') end
   if redis.call('HGET',KEYS[i+1],'generation')~=ARGV[math.floor(i/2)] then return redis.error_reply('Exit generation changed') end
   if redis.call('HGET',KEYS[i],'generation')~=ARGV[math.floor(i/2)] then return redis.error_reply('Exit report superseded') end
   local reported=tonumber(redis.call('HGET',KEYS[i],'reported_at') or '-1')
   if reported<0 then return redis.error_reply('missing report timestamp') end
   local report=redis.call('HGET',KEYS[i],'uuids')
   if not report then return redis.error_reply('missing Exit association data') end
   if reported<minPublished then minPublished=reported end
   local ids=cjson.decode(report)
   for _,id in ipairs(ids) do live[id]=true end
 end
 local current=redis.call('SMEMBERS',KEYS[1]);local removed={}
 for _,id in ipairs(current) do
   local absentKey=KEYS[1]..':absence:'..id
   if live[id] then
     redis.call('DEL',absentKey)
   else
     local nonce=redis.call('HGET',KEYS[1]..':reservations',id)
     local reserved=tonumber(redis.call('HGET',KEYS[1]..':reserved_at',id) or '-1')
     if not nonce or reserved<0 then return redis.error_reply('missing reservation fence') end
     local seen=redis.call('HMGET',absentKey,'since','nonce')
     if minPublished<=reserved then redis.call('DEL',absentKey)
     elseif not seen[1] or seen[2]~=nonce then
       redis.call('HSET',absentKey,'since',ms,'nonce',nonce)
     elseif ms-tonumber(seen[1])>=30000 then
       redis.call('SREM',KEYS[1],id)
       redis.call('HDEL',KEYS[1]..':reservations',id)
       redis.call('HDEL',KEYS[1]..':reserved_at',id)
       redis.call('DEL',absentKey)
       table.insert(removed,id)
     end
   end
 end
 return removed`)
	result, err := script.Run(ctx, rdb, keys, args...).StringSlice()
	if err != nil {
		return nil, err
	}
	sort.Strings(result)
	return result, nil
}
