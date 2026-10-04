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
//
// plan_orders tracking fields (origin, client_ip, user_agent, browser_family,
// os_family, locale, device_fingerprint) are purged under DEC-027 after 180 days
// by NULLing them rather than deleting rows, preserving all financial evidence:
// order status/pricing, payment provider events, and plan grant timestamps.
const (
	trafficLogRetention       = 90 * 24 * time.Hour
	nodeMetricsRetention      = 14 * 24 * time.Hour
	activityLogRetention      = 90 * 24 * time.Hour
	snapshotRetention         = 30 * 24 * time.Hour
	loginHistoryRetention     = 180 * 24 * time.Hour
	planOrdersPurgeRetention  = 180 * 24 * time.Hour
	retentionSweepInterval    = 6 * time.Hour

	// deleteBatchSize bounds each DELETE so a first sweep over a large backlog
	// cannot hold locks or bloat WAL for long; the sweep repeats until a batch
	// comes back short.
	deleteBatchSize = 20000

	// updateBatchSize bounds each UPDATE so NULLing tracking fields does not hold
	// locks long; the sweep repeats until a batch comes back short.
	updateBatchSize = 20000
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
	s.prune(ctx, "activity_logs", "created_at", activityLogRetention)
	s.prune(ctx, "dashboard_snapshots", "recorded_at", snapshotRetention)
	// login_history is append-only and fed by an unauthenticated endpoint, so it
	// grows without a sweep. Kept longer than the activity feed because it is the
	// audit trail an operator reaches for after the fact.
	s.prune(ctx, "login_history", "created_at", loginHistoryRetention)
	// plan_orders tracking fields (checkout audit) are NULLed rather than deleted
	// to preserve all financial evidence (order status, payment events, grant timestamps).
	s.purgeColumns(ctx)
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

// purgeColumns NULLs checkout audit tracking fields in plan_orders older than
// the retention window, preserving all financial evidence: order/payment status,
// provider events, and plan grant timestamps. The partial index idx_plan_orders_purge_sweep
// (WHERE user_agent IS NOT NULL) keeps scans off full table when user_agent becomes NULL.
func (s *RetentionScheduler) purgeColumns(ctx context.Context) {
	// Tracking columns to NULL: origin, client_ip, user_agent, browser_family,
	// os_family, locale, device_fingerprint. Financial columns preserved:
	// status, price_cents, paid_at, paid_by, cancelled_at, provider, provider_session_id,
	// promotion_id, discount_cents, granted_plan_expires_before, granted_plan_expires_after.
	query := `UPDATE plan_orders
		SET origin = NULL,
		    client_ip = NULL,
		    user_agent = NULL,
		    browser_family = NULL,
		    os_family = NULL,
		    locale = NULL,
		    device_fingerprint = NULL
		WHERE ctid IN (
			SELECT ctid FROM plan_orders
			WHERE created_at < $1 AND user_agent IS NOT NULL
			LIMIT ` + strconv.Itoa(updateBatchSize) + `
		)`

	cutoff := time.Now().Add(-planOrdersPurgeRetention)
	var total int64

	for ctx.Err() == nil {
		result, err := s.db.Exec(ctx, query, cutoff)
		if err != nil {
			log.Printf("[Retention] error purging plan_orders tracking: %v", err)
			return
		}

		affected := result.RowsAffected()
		total += affected
		if affected < updateBatchSize {
			break
		}
	}

	if total > 0 {
		log.Printf("[Retention] purged tracking from %d plan_orders row(s) older than %s", total, planOrdersPurgeRetention)
	}
}
