package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// BeginExitReportGeneration fences delayed HTTP reports from a prior node-agent
// process. A started agent announces its random generation before its first
// report; a stale generation may never overwrite the new process report.
func BeginExitReportGeneration(ctx context.Context, rdb *redis.Client, nodeID, generation string) error {
	if rdb == nil {
		return fmt.Errorf("report Redis unavailable")
	}
	if _, err := uuid.Parse(generation); err != nil {
		return err
	}
	key := "node:" + nodeID + ":admitted"
	return redis.NewScript(`
 local epoch=redis.call('INCR',KEYS[1])
 redis.call('HSET',KEYS[2],'generation',ARGV[1],'epoch',epoch,'started_at',ARGV[2])
 redis.call('DEL',KEYS[3])
 return epoch`).Run(ctx, rdb, []string{key + ":epoch", key + ":fence", key}, generation, time.Now().UnixMilli()).Err()
}
