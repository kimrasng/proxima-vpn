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
	cancel   context.CancelFunc
}

// NewNodeMonitorScheduler creates a NodeMonitorScheduler.
func NewNodeMonitorScheduler(db *pgxpool.Pool, telegram *services.TelegramService) *NodeMonitorScheduler {
	return &NodeMonitorScheduler{db: db, telegram: telegram}
}

// Start runs the node monitor every 60 seconds.
func (s *NodeMonitorScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(60 * time.Second)
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
		SET status = 'offline', updated_at = NOW()
		WHERE status = 'online'
		  AND last_seen < NOW() - INTERVAL '90 seconds'
		RETURNING name
	`)
	if err != nil {
		log.Printf("[NodeMonitor] error detecting offline nodes: %v", err)
		return
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		count++
		if err := s.telegram.NotifyNodeOffline(ctx, name); err != nil {
			log.Printf("[NodeMonitor] telegram alert failed for %s: %v", name, err)
		}
	}

	if count > 0 {
		log.Printf("[NodeMonitor] marked %d node(s) offline", count)
	}
}
