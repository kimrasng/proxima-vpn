package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/redis/go-redis/v9"
)

const snapshotInterval = 15 * time.Minute

// SnapshotScheduler records the dashboard KPIs periodically so the panel can
// show period-over-period deltas.
type SnapshotScheduler struct {
	db      *pgxpool.Pool
	stats   *services.StatsService
	tracker *services.OnlineTracker
	cancel  context.CancelFunc
}

// NewSnapshotScheduler creates a SnapshotScheduler.
func NewSnapshotScheduler(db *pgxpool.Pool, rdb *redis.Client) *SnapshotScheduler {
	return &SnapshotScheduler{
		db:      db,
		stats:   services.NewStatsService(db),
		tracker: services.NewOnlineTracker(rdb),
	}
}

// Start records a snapshot immediately and then every snapshotInterval.
func (s *SnapshotScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()

	log.Printf("[Snapshot] started (every %s)", snapshotInterval)
	s.run(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[Snapshot] stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

// Stop cancels the scheduler context.
func (s *SnapshotScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *SnapshotScheduler) run(ctx context.Context) {
	summary, err := s.stats.GetSummary(ctx)
	if err != nil {
		log.Printf("[Snapshot] skipping: %v", err)
		return
	}

	alerts, err := s.stats.GetAlerts(ctx)
	if err != nil {
		log.Printf("[Snapshot] skipping: %v", err)
		return
	}

	var trafficToday int64
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(up_bytes + dn_bytes), 0) FROM traffic_logs
		WHERE created_at >= CURRENT_DATE
	`).Scan(&trafficToday); err != nil {
		log.Printf("[Snapshot] skipping: %v", err)
		return
	}

	onlineUsers, err := s.tracker.GetAllOnlineCount(ctx)
	if err != nil {
		onlineUsers = 0
	}

	if err := s.stats.RecordSnapshot(ctx, services.SnapshotValues{
		ActiveAlerts: alerts.Total,
		OnlineNodes:  int(summary.OnlineNodes),
		TotalNodes:   int(summary.TotalNodes),
		OnlineUsers:  onlineUsers,
		TotalUsers:   int(summary.TotalUsers),
		TrafficToday: trafficToday,
	}); err != nil {
		log.Printf("[Snapshot] error: %v", err)
	}
}
