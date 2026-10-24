package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExpiryCheckScheduler disables users whose plan expired.
//
// It deliberately does not suspend accounts that used up their data. Every
// path that hands out VPN access (node configs, WireGuard/Hysteria2 peers,
// subscriptions) already refuses a user at or over traffic_limit, so the cap
// is enforced without touching the account. Suspending it as well locked the
// user out of the portal, where they need to sign in to see their usage, buy
// more time or spend a reset ticket, and set is_active = false, which also
// kept TrafficResetScheduler from ever lifting the cap at the monthly reset.
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

	if expired := expiredResult.RowsAffected(); expired > 0 {
		log.Printf("[ExpiryCheckScheduler] expired=%d", expired)
	}
}
