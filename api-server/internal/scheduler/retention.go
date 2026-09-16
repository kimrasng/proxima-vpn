package scheduler

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Retention windows for the two append-only tables the node-agent feeds. Both
// previously grew without bound - a heartbeat row per node per 30s, plus a
// traffic row per active device per 30s - which eventually slows the joins that
// every config poll and subscription fetch depend on. traffic_logs is kept
// longer because it backs the admin traffic charts; users.traffic_used holds the
// authoritative total, so pruning the log does not lose quota accounting.
const (
	trafficLogRetention    = 90 * 24 * time.Hour
	nodeMetricsRetention   = 14 * 24 * time.Hour
	retentionSweepInterval = 6 * time.Hour

	// deleteBatchSize bounds each DELETE so a first sweep over a large backlog
	// cannot hold locks or bloat WAL for long; the sweep repeats until a batch
	// comes back short.
	deleteBatchSize = 20000
)

// RetentionScheduler trims append-only telemetry tables to a bounded window.
type RetentionScheduler struct {
	db     *pgxpool.Pool
	cancel context.CancelFunc
}

// NewRetentionScheduler creates a new RetentionScheduler.
func NewRetentionScheduler(db *pgxpool.Pool) *RetentionScheduler {
	return &RetentionScheduler{db: db}
}

// Start runs a retention sweep immediately and then every retentionSweepInterval.
func (s *RetentionScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(retentionSweepInterval)
	defer ticker.Stop()

	log.Printf("[Retention] started (traffic_logs %s, node_metrics_history %s)",
		trafficLogRetention, nodeMetricsRetention)
	s.run(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[Retention] stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

// Stop cancels the scheduler context.
func (s *RetentionScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *RetentionScheduler) run(ctx context.Context) {
	s.prune(ctx, "traffic_logs", "created_at", trafficLogRetention)
	s.prune(ctx, "node_metrics_history", "recorded_at", nodeMetricsRetention)
}

// prune deletes rows older than the retention window in bounded batches. table
// and column come from run's fixed call sites, never from request input.
func (s *RetentionScheduler) prune(ctx context.Context, table, column string, window time.Duration) {
	query := `DELETE FROM ` + table + `
		WHERE ctid IN (
			SELECT ctid FROM ` + table + `
			WHERE ` + column + ` < $1
			LIMIT ` + strconv.Itoa(deleteBatchSize) + `
		)`

	cutoff := time.Now().Add(-window)
	var total int64

	for ctx.Err() == nil {
		result, err := s.db.Exec(ctx, query, cutoff)
		if err != nil {
			log.Printf("[Retention] error pruning %s: %v", table, err)
			return
		}

		affected := result.RowsAffected()
		total += affected
		if affected < deleteBatchSize {
			break
		}
	}

	if total > 0 {
		log.Printf("[Retention] pruned %d row(s) from %s older than %s", total, table, window)
	}
}
