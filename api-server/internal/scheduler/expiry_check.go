package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExpiryCheckScheduler disables users whose plan expired or traffic exceeded.
type ExpiryCheckScheduler struct {
	db     *pgxpool.Pool
	cancel context.CancelFunc
}

// NewExpiryCheckScheduler creates a new ExpiryCheckScheduler.
func NewExpiryCheckScheduler(db *pgxpool.Pool) *ExpiryCheckScheduler {
	return &ExpiryCheckScheduler{db: db}
}

// Start runs the expiry check every 5 minutes.
func (s *ExpiryCheckScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	log.Println("[ExpiryCheckScheduler] started")
	s.run(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[ExpiryCheckScheduler] stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

// Stop cancels the scheduler context.
func (s *ExpiryCheckScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *ExpiryCheckScheduler) run(ctx context.Context) {
	expiredResult, err := s.db.Exec(ctx, `
		UPDATE users
		SET status = 'expired', is_active = false, updated_at = NOW()
		WHERE plan_expires_at < NOW()
		  AND status = 'active'
		  AND is_active = true
	`)
	if err != nil {
		log.Printf("[ExpiryCheckScheduler] error expiring users: %v", err)
		return
	}

	suspendedResult, err := s.db.Exec(ctx, `
		UPDATE users u
		SET status = 'suspended', is_active = false, updated_at = NOW()
		FROM plans p
		WHERE u.plan_id = p.id
		  AND u.traffic_used >= p.traffic_limit
		  AND u.status = 'active'
		  AND u.is_active = true
		  AND p.traffic_limit > 0
	`)
	if err != nil {
		log.Printf("[ExpiryCheckScheduler] error suspending over-limit users: %v", err)
		return
	}

	expired := expiredResult.RowsAffected()
	suspended := suspendedResult.RowsAffected()
	if expired > 0 || suspended > 0 {
		log.Printf("[ExpiryCheckScheduler] expired=%d suspended=%d", expired, suspended)
	}
}
