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

// run recurs the monthly reset on the anniversary of plan_started_at rather
// than matching today's day-of-month against it: a plan started on the 31st
// otherwise never resets in a 30-day month, since no day in that month equals
// 31. Anchoring on whole months elapsed since plan_started_at and letting
// Postgres's interval arithmetic clamp the result to the month's last day
// (make_interval(months) on the 31st lands on Feb 28, then back to the 31st in
// March) reproduces the same anniversary every month without that gap.
//
// NOW() < plan_expires_at is a hard ceiling against the term itself, not
// against status: status is a cache ExpiryCheckScheduler writes on its own
// 5-minute tick, so gating on it would let a reset land after expiry if that
// tick hasn't run yet. Time is the source of truth here.
//
// Dropping the status = 'active' filter that the old query had also fixes a
// second bug: a user ExpiryCheckScheduler suspended for hitting traffic_limit
// would never see a reset (which is the mechanism meant to lift that cap)
// until their term ended. The un-suspend in SET is the fix; other statuses
// ('expired', anything admin-set) are left alone.
func (s *TrafficResetScheduler) run(ctx context.Context) {
	result, err := s.db.Exec(ctx, `
		UPDATE users u
		SET traffic_used     = 0,
		    traffic_reset_at = NOW(),
		    status           = CASE WHEN u.status = 'suspended' THEN 'active' ELSE u.status END,
		    updated_at       = NOW()
		FROM (
		  SELECT s.id,
		         CASE WHEN s.plan_started_at + make_interval(months => s.months_elapsed) <= NOW()
		              THEN s.plan_started_at + make_interval(months => s.months_elapsed)
		              ELSE s.plan_started_at + make_interval(months => s.months_elapsed - 1)
		         END AS period_start
		  FROM (
		    SELECT id, plan_started_at,
		           (DATE_PART('year', NOW()) - DATE_PART('year', plan_started_at))::int * 12
		         + (DATE_PART('month', NOW()) - DATE_PART('month', plan_started_at))::int AS months_elapsed
		    FROM users
		    WHERE is_active = true
		      AND plan_id IS NOT NULL
		      AND plan_started_at IS NOT NULL
		      AND plan_expires_at IS NOT NULL
		      AND plan_started_at <= NOW()
		      AND NOW() < plan_expires_at
		  ) s
		) w
		WHERE u.id = w.id
		  AND COALESCE(u.traffic_reset_at, u.plan_started_at) < w.period_start
	`)
	if err != nil {
		log.Printf("[TrafficResetScheduler] error resetting traffic: %v", err)
		return
	}

	if result.RowsAffected() > 0 {
		log.Printf("[TrafficResetScheduler] reset traffic for %d users", result.RowsAffected())
	}
}
