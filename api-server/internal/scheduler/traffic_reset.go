package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TrafficResetScheduler resets monthly traffic on each user's plan start day.
type TrafficResetScheduler struct {
	db     *pgxpool.Pool
	cancel context.CancelFunc
}

// NewTrafficResetScheduler creates a new TrafficResetScheduler.
func NewTrafficResetScheduler(db *pgxpool.Pool) *TrafficResetScheduler {
	return &TrafficResetScheduler{db: db}
}

// Start runs the traffic reset check every hour.
func (s *TrafficResetScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Println("[TrafficResetScheduler] started")
	s.run(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[TrafficResetScheduler] stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

// Stop cancels the scheduler context.
func (s *TrafficResetScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *TrafficResetScheduler) run(ctx context.Context) {
	today := time.Now().Day()

	result, err := s.db.Exec(ctx, `
		UPDATE users
		SET traffic_used = 0, traffic_reset_at = NOW(), updated_at = NOW()
		WHERE EXTRACT(DAY FROM plan_started_at) = $1
		  AND status = 'active'
		  AND is_active = true
		  AND (traffic_reset_at IS NULL OR traffic_reset_at < NOW() - INTERVAL '25 days')
	`, today)
	if err != nil {
		log.Printf("[TrafficResetScheduler] error resetting traffic: %v", err)
		return
	}

	if result.RowsAffected() > 0 {
		log.Printf("[TrafficResetScheduler] reset traffic for %d users", result.RowsAffected())
	}
}
