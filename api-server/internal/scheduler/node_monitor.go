package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// NodeMonitorScheduler marks unresponsive nodes offline and then evaluates every
// alert condition, so the alert set can never disagree with the node status it
// was derived from.
type NodeMonitorScheduler struct {
	db       *pgxpool.Pool
	telegram *services.TelegramService
	activity *services.ActivityService
	alerts   *services.AlertService
	cancel   context.CancelFunc
}

// NewNodeMonitorScheduler creates a NodeMonitorScheduler.
func NewNodeMonitorScheduler(db *pgxpool.Pool, telegram *services.TelegramService) *NodeMonitorScheduler {
	return &NodeMonitorScheduler{
		db:       db,
		telegram: telegram,
		activity: services.NewActivityService(db),
		alerts:   services.NewAlertService(db),
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
	if _, err := s.db.Exec(ctx, `
		UPDATE nodes
		SET status = 'offline', updated_at = NOW()
		WHERE status = 'online'
		  -- Four missed 10s heartbeats (node-agent heartbeatInterval). The old
		  -- 90s was three beats when beats were 30s apart; keeping it would now
		  -- mean sitting on a dead node for nine.
		  AND last_seen < NOW() - INTERVAL '40 seconds'
	`); err != nil {
		log.Printf("[NodeMonitor] error marking offline nodes: %v", err)
		return
	}

	// Evaluated after the sweep, in the same tick, because it reads the status the
	// sweep just wrote. The offline edge is reported here rather than from the
	// UPDATE, so one condition cannot produce two notifications.
	transitions, err := s.alerts.Evaluate(ctx)
	if err != nil {
		log.Printf("[NodeMonitor] alert evaluation failed: %v", err)
		return
	}

	for _, t := range transitions {
		s.record(ctx, t)
	}
}

// record logs a transition to the activity feed and notifies for it, unless the
// transition is a seeded first observation, is inside the startup grace window,
// or the operator has silenced this node and kind.
func (s *NodeMonitorScheduler) record(ctx context.Context, t services.AlertTransition) {
	firing := t.To == services.AlertStateFiring

	eventType := services.EventNodeAlertFired
	severity := t.Severity
	if !firing {
		eventType = services.EventNodeAlertResolved
		severity = services.SeveritySuccess
	}
	// The offline edge keeps its long-standing event names so existing feed
	// entries and their translations stay meaningful.
	if t.Kind == services.AlertOffline {
		if firing {
			eventType = services.EventNodeOffline
		} else {
			eventType = services.EventNodeOnline
		}
	}

	detail := map[string]any{"node": t.NodeName, "kind": string(t.Kind)}
	if t.Value > 0 {
		detail["value"] = t.Value
	}
	if !firing && t.Duration > 0 {
		detail["duration_seconds"] = int64(t.Duration.Seconds())
	}

	s.activity.Log(ctx, services.Record{
		EventType:  eventType,
		Severity:   severity,
		ActorType:  "system",
		ActorLabel: t.NodeName,
		TargetType: "node",
		TargetID:   t.NodeID,
		Detail:     detail,
	})

	if !t.Notify || s.alerts.Silenced(ctx, t.NodeID, t.Kind) {
		return
	}
	if err := s.telegram.SendAlert(ctx, alertMessage(t)); err != nil {
		log.Printf("[NodeMonitor] telegram alert failed for %s/%s: %v", t.NodeName, t.Kind, err)
	}
}

func alertMessage(t services.AlertTransition) string {
	if t.To != services.AlertStateFiring {
		if t.Duration > 0 {
			return fmt.Sprintf("\u2705 <b>Recovered</b>\n<code>%s</code> %s cleared after %s.",
				t.NodeName, t.Kind, t.Duration.Round(time.Second))
		}
		return fmt.Sprintf("\u2705 <b>Recovered</b>\n<code>%s</code> %s cleared.", t.NodeName, t.Kind)
	}
	switch t.Kind {
	case services.AlertOffline:
		return fmt.Sprintf("\U0001F534 <b>Node Offline</b>\nNode <code>%s</code> is no longer responding.", t.NodeName)
	case services.AlertXrayDown:
		return fmt.Sprintf("\U0001F534 <b>Xray Down</b>\nNode <code>%s</code> is reporting but Xray is not running.", t.NodeName)
	case services.AlertShapingFailed:
		return fmt.Sprintf("\u26A0\uFE0F <b>Shaping Failed</b>\nNode <code>%s</code> could not apply its speed limits.", t.NodeName)
	default:
		return fmt.Sprintf("\u26A0\uFE0F <b>%s High</b>\nNode <code>%s</code> is at %.0f%%.", t.Kind, t.NodeName, t.Value)
	}
}
