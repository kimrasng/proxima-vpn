package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// OrderExpiryScheduler flips a pending plan_orders row to 'expired' once its
// deadline passes. Expiring is only a timeout, not a refusal: PaymentService
// still allows an 'expired' order to settle if a confirmation arrives after
// the sweep, since a hosted checkout session that completed just past the
// deadline is still money actually taken. The sweeper's own job is narrower -
// free the user's one-pending-order slot so an abandoned checkout does not
// block them from placing a new order.
type OrderExpiryScheduler struct {
	db        *pgxpool.Pool
	act       *services.ActivityService
	promotion *services.PromotionService
	cancel    context.CancelFunc
}

// NewOrderExpiryScheduler creates a new OrderExpiryScheduler.
func NewOrderExpiryScheduler(db *pgxpool.Pool) *OrderExpiryScheduler {
	return &OrderExpiryScheduler{db: db, act: services.NewActivityService(db), promotion: services.NewPromotionService(db)}
}

// Start runs the expiry sweep every 5 minutes, matching ExpiryCheckScheduler.
func (s *OrderExpiryScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	log.Println("[OrderExpiryScheduler] started")
	s.run(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("[OrderExpiryScheduler] stopped")
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

// Stop cancels the scheduler context.
func (s *OrderExpiryScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *OrderExpiryScheduler) run(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
		UPDATE plan_orders
		SET status = 'expired', expired_at = NOW()
		WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at < NOW()
		RETURNING id, user_id
	`)
	if err != nil {
		log.Printf("[OrderExpiryScheduler] error expiring orders: %v", err)
		return
	}

	type expired struct{ id, userID string }
	var results []expired
	for rows.Next() {
		var e expired
		if err := rows.Scan(&e.id, &e.userID); err != nil {
			rows.Close()
			log.Printf("[OrderExpiryScheduler] error scanning expired order: %v", err)
			return
		}
		results = append(results, e)
	}
	rows.Close()

	for _, e := range results {
		if err := s.promotion.Release(ctx, e.id); err != nil {
			log.Printf("[OrderExpiryScheduler] error releasing promotion for order %s: %v", e.id, err)
		}
		s.act.Log(ctx, services.Record{
			EventType:  services.EventOrderExpired,
			Severity:   services.SeverityInfo,
			ActorType:  "system",
			TargetType: "plan_order",
			TargetID:   e.id,
			Detail:     map[string]any{"user_id": e.userID},
		})
	}

	if len(results) > 0 {
		log.Printf("[OrderExpiryScheduler] expired %d orders", len(results))
	}
}
