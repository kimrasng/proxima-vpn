package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// NodeMonitorScheduler detects offline nodes and sends alerts.
type NodeMonitorScheduler struct {
	db       *pgxpool.Pool
	telegram *services.TelegramService
	activity *services.ActivityService
	cancel   context.CancelFunc
}

// NewNodeMonitorScheduler creates a NodeMonitorScheduler.
func NewNodeMonitorScheduler(db *pgxpool.Pool, telegram *services.TelegramService) *NodeMonitorScheduler {
	return &NodeMonitorScheduler{
		db:       db,
		telegram: telegram,
		activity: services.NewActivityService(db),
	}
}

// offlineSweepInterval bounds how long a dead node keeps reading as online:
// worst case is this plus the staleness window in run(). At the old 60s a 40s
// window would still have taken 100s to notice.
const offlineSweepInterval = 15 * time.Second

// Start sweeps for nodes that stopped reporting, every offlineSweepInterval.
func (s *NodeMonitorScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(offlineSweepInterval)
	defer ticker.Stop()

	log.Println("[NodeMonitor] started")

	for {
		select {
		case <-ctx.Done():
			log.Println("[NodeMonitor] stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

// Stop cancels the node monitor.
func (s *NodeMonitorScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *NodeMonitorScheduler) run(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
		UPDATE nodes
		SET status = 'offline', updated_at = NOW(), status_changed_at = NOW()
		WHERE status = 'online'
		  -- Four missed 10s heartbeats (node-agent heartbeatInterval). The old
		  -- 90s was three beats when beats were 30s apart; keeping it would now
		  -- mean sitting on a dead node for nine.
		  AND last_seen < NOW() - INTERVAL '40 seconds'
		RETURNING id::text, name
	`)
	if err != nil {
		log.Printf("[NodeMonitor] error detecting offline nodes: %v", err)
		return
	}

	type offlineNode struct {
		id   string
		name string
	}
	// Collected before the notify/log calls rather than inside the loop: both
	// write to the same pool, and holding the UPDATE ... RETURNING cursor open
	// while issuing them can exhaust it and stall the sweep.
	var offline []offlineNode
	for rows.Next() {
		var n offlineNode
		if err := rows.Scan(&n.id, &n.name); err != nil {
			continue
		}
		offline = append(offline, n)
	}
	rows.Close()

	count := len(offline)
	for _, n := range offline {
		s.activity.Log(ctx, services.Record{
			EventType:  services.EventNodeOffline,
			Severity:   services.SeverityError,
			ActorType:  "system",
			ActorLabel: n.name,
			TargetType: "node",
			TargetID:   n.id,
			Detail:     map[string]any{"node": n.name},
		})
		if err := s.telegram.NotifyNodeOffline(ctx, n.name); err != nil {
			log.Printf("[NodeMonitor] telegram alert failed for %s: %v", n.name, err)
		}
	}

	if count > 0 {
		log.Printf("[NodeMonitor] marked %d node(s) offline", count)
	}
}
