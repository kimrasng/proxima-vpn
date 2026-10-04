package scheduler

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// seedExpiryOrder inserts a plan_orders row with the given status and
// expires_at, cleaning it up on test completion.
func seedExpiryOrder(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID, planID string, status string, expiresAt *time.Time) string {
	t.Helper()

	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status, expires_at)
		 VALUES ($1, $2, 30, 1000, $3, $4) RETURNING id::text`,
		userID, planID, status, expiresAt,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM plan_orders WHERE id = $1`, orderID) })
	return orderID
}

func seedExpiryPromotion(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code string) string {
	t.Helper()

	var promotionID string
	now := time.Now()
	if err := pool.QueryRow(ctx,
		`INSERT INTO promotion_codes
		 (code, discount_type, discount_value, valid_from, valid_until, max_redemptions_per_user)
		 VALUES ($1, 'fixed', 100, $2, $3, 1) RETURNING id::text`,
		code, now.Add(-time.Hour), now.Add(time.Hour),
	).Scan(&promotionID); err != nil {
		t.Fatalf("seed promotion: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM promotion_codes WHERE id = $1`, promotionID)
	})
	return promotionID
}

// A pending order past its deadline must be swept to 'expired' - this frees
// the one-pending-order slot for an abandoned checkout.
func TestOrderExpiry_SweepsPendingPastDeadline(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewOrderExpiryScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("orderexp-past-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("orderexp-past-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "pending")
	past := time.Now().Add(-1 * time.Hour)
	orderID := seedExpiryOrder(t, pool, ctx, userID, planID, "pending", &past)

	sched.run(ctx)

	var status string
	var expiredAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, expired_at FROM plan_orders WHERE id = $1`, orderID).Scan(&status, &expiredAt); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "expired" {
		t.Errorf("status = %q, want expired", status)
	}
	if expiredAt == nil {
		t.Error("expired_at is nil, want set")
	}
}

func TestOrderExpiry_ReleasesPromotionReservationOnlyOnce(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewOrderExpiryScheduler(pool)
	promotionService := services.NewPromotionService(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("orderexp-promo-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("orderexp-promo-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "pending")
	past := time.Now().Add(-time.Hour)
	orderID := seedExpiryOrder(t, pool, ctx, userID, planID, "pending", &past)
	promotionID := seedExpiryPromotion(t, pool, ctx, fmt.Sprintf("EXPIRE-%d", os.Getpid()))
	if _, err := promotionService.Reserve(ctx, fmt.Sprintf("EXPIRE-%d", os.Getpid()), userID, orderID, planID, 30, 1000, true); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	sched.run(ctx)
	sched.run(ctx)

	var redemptionStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM promotion_redemptions WHERE order_id = $1`, orderID).Scan(&redemptionStatus); err != nil {
		t.Fatalf("read redemption status: %v", err)
	}
	if redemptionStatus != "released" {
		t.Errorf("redemption status = %q, want released", redemptionStatus)
	}
	var redeemedCount int
	if err := pool.QueryRow(ctx, `SELECT redeemed_count FROM promotion_codes WHERE id = $1`, promotionID).Scan(&redeemedCount); err != nil {
		t.Fatalf("read redeemed count: %v", err)
	}
	if redeemedCount != 0 {
		t.Errorf("redeemed_count = %d, want 0 after exactly one release", redeemedCount)
	}
}

// A pending order whose deadline has not yet passed must not be touched.
func TestOrderExpiry_DoesNotTouchOrderNotYetDue(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewOrderExpiryScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("orderexp-future-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("orderexp-future-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "pending")
	future := time.Now().Add(1 * time.Hour)
	orderID := seedExpiryOrder(t, pool, ctx, userID, planID, "pending", &future)

	sched.run(ctx)

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM plan_orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "pending" {
		t.Errorf("status = %q, want unchanged pending", status)
	}
}

// A paid order past what was once its deadline must never be swept - the
// sweeper only ever moves pending orders, since paid/cancelled/expired are
// already resolved.
func TestOrderExpiry_DoesNotTouchPaidOrder(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewOrderExpiryScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("orderexp-paid-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("orderexp-paid-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "active")
	past := time.Now().Add(-1 * time.Hour)
	orderID := seedExpiryOrder(t, pool, ctx, userID, planID, "paid", &past)

	sched.run(ctx)

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM plan_orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "paid" {
		t.Errorf("status = %q, want unchanged paid", status)
	}
}
