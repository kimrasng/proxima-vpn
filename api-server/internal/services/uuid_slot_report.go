package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const admittedReportTTL = 30 * time.Second

// PublishAdmittedUUIDReport atomically replaces an Exit's *open* UUID set. A
// sequence number fences late HTTP requests from older polling generations.
// A missing report is unknown (not empty) and cannot release an account slot.
func PublishAdmittedUUIDReport(ctx context.Context, rdb *redis.Client, nodeID, generation string, sequence int64, uuids []string, now time.Time) error {
	if rdb == nil {
		return fmt.Errorf("admitted UUID Redis unavailable")
	}
	data, err := json.Marshal(uuids)
	if err != nil {
		return err
	}
	key := "node:" + nodeID + ":admitted"
	// Node process generation is a random UUID; timestamps alone cannot order
	// a restarted agent's reports. A generation change invalidates only that
	// node's previous report, while account slot reconciliation still waits for
	// complete reports from the whole Exit fleet.
	result, err := redis.NewScript(`
 local authorized=redis.call('HGET',KEYS[2],'generation')
 if authorized~=ARGV[1] then return redis.error_reply('stale agent generation') end
 local generation=redis.call('HGET',KEYS[1],'generation')
 local seq=tonumber(ARGV[2])
 local oldSeq=tonumber(redis.call('HGET',KEYS[1],'sequence') or '-1')
 if generation==ARGV[1] and seq<=oldSeq then return 0 end
 local clock=redis.call('TIME');local publishedAt=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
 redis.call('HSET',KEYS[1],'generation',ARGV[1],'sequence',seq,'uuids',ARGV[3],'reported_at',publishedAt)
 redis.call('PEXPIRE',KEYS[1],ARGV[4]);return 1`).Run(ctx, rdb, []string{key, key + ":fence"}, generation, sequence, string(data), int64(admittedReportTTL/time.Millisecond)).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return fmt.Errorf("stale admitted report")
	}
	return nil
}
