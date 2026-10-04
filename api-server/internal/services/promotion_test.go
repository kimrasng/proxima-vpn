package services_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// seedPromotion inserts a promotion_codes row with the given caps and window,
// cleaning it up on test completion. maxRedemptions nil means unlimited.
func seedPromotion(t *testing.T, pool *pgxpool.Pool, ctx context.Context, code, discountType string, discountValue, minOrderCents int64, maxRedemptions *int, maxPerUser int, validFrom, validUntil time.Time) string {
	t.Helper()

	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO promotion_codes
		   (code, discount_type, discount_value, valid_from, valid_until, min_order_cents,
		    max_redemptions, max_redemptions_per_user)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id::text`,
		code, discountType, discountValue, validFrom, validUntil, minOrderCents,
		maxRedemptions, maxPerUser,
	).Scan(&id); err != nil {
		t.Fatalf("seed promotion: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM promotion_codes WHERE id = $1`, id) })
	return id
}

func redeemedCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, promotionID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT redeemed_count FROM promotion_codes WHERE id = $1`, promotionID).Scan(&n); err != nil {
		t.Fatalf("read redeemed_count: %v", err)
	}
	return n
}

func redemptionStatus(t *testing.T, pool *pgxpool.Pool, ctx context.Context, orderID string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM promotion_redemptions WHERE order_id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read redemption status: %v", err)
	}
	return status
}

func TestReserve_AppliesPercentDiscount(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("percent-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")
	promoID := seedPromotion(t, pool, ctx, "PERCENT10-"+pid, "percent", 10, 0, nil, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	reservation, err := svc.Reserve(ctx, "PERCENT10-"+pid, userID, orderID, planID, 30, 1000, true)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if reservation.DiscountCents != 100 {
		t.Errorf("expected 100 cent discount on a 1000 cent order at 10%%, got %d", reservation.DiscountCents)
	}
	if reservation.PromotionID != promoID {
		t.Errorf("expected promotion id %s, got %s", promoID, reservation.PromotionID)
	}
	if got := redeemedCount(t, pool, ctx, promoID); got != 1 {
		t.Errorf("expected redeemed_count 1, got %d", got)
	}
	if got := redemptionStatus(t, pool, ctx, orderID); got != "reserved" {
		t.Errorf("expected redemption status reserved, got %s", got)
	}
}

func TestReserve_FixedDiscountNeverGoesNegative(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("fixed-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")
	seedPromotion(t, pool, ctx, "BIGFIXED-"+pid, "fixed", 5000, 0, nil, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	reservation, err := svc.Reserve(ctx, "BIGFIXED-"+pid, userID, orderID, planID, 30, 1000, true)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if reservation.DiscountCents != 1000 {
		t.Errorf("expected discount capped at the order total (1000), got %d", reservation.DiscountCents)
	}
}

func TestReserve_RejectsOutsideValidityWindow(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("window-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")
	seedPromotion(t, pool, ctx, "EXPIRED-"+pid, "fixed", 100, 0, nil, 1,
		time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	_, err := svc.Reserve(ctx, "EXPIRED-"+pid, userID, orderID, planID, 30, 1000, true)
	assertRejectReason(t, err, services.RejectOutsideWindow)
}

func TestReserve_RejectsBelowMinimumOrder(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("minorder-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")
	seedPromotion(t, pool, ctx, "MIN50-"+pid, "fixed", 100, 5000, nil, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	_, err := svc.Reserve(ctx, "MIN50-"+pid, userID, orderID, planID, 30, 1000, true)
	assertRejectReason(t, err, services.RejectBelowMinimum)
}

func TestReserve_OverallCapBlocksTheRedemptionAfterItIsReached(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("cap-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	capacity := 1
	seedPromotion(t, pool, ctx, "ONESHOT-"+pid, "fixed", 100, 0, &capacity, 5,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	userA := seedUser(t, pool, ctx, pid+"-a@test.com", planID, nil, nil, nil, 0, "active")
	userB := seedUser(t, pool, ctx, pid+"-b@test.com", planID, nil, nil, nil, 0, "active")
	orderA := seedOrder(t, pool, ctx, userA, planID, 30, 1000, "pending", nil)
	orderB := seedOrder(t, pool, ctx, userB, planID, 30, 1000, "pending", nil)

	if _, err := svc.Reserve(ctx, "ONESHOT-"+pid, userA, orderA, planID, 30, 1000, true); err != nil {
		t.Fatalf("first Reserve should succeed: %v", err)
	}

	_, err := svc.Reserve(ctx, "ONESHOT-"+pid, userB, orderB, planID, 30, 1000, true)
	assertRejectReason(t, err, services.RejectOverallCapReached)
}

func TestReserve_PerUserCapBlocksASecondRedemptionByTheSameUser(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("usercap-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")
	seedPromotion(t, pool, ctx, "ONEUSER-"+pid, "fixed", 100, 0, nil, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	order1 := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "cancelled", nil)
	order2 := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	if _, err := svc.Reserve(ctx, "ONEUSER-"+pid, userID, order1, planID, 30, 1000, true); err != nil {
		t.Fatalf("first Reserve should succeed: %v", err)
	}

	_, err := svc.Reserve(ctx, "ONEUSER-"+pid, userID, order2, planID, 30, 1000, true)
	assertRejectReason(t, err, services.RejectUserCapReached)
}

func TestReserve_RejectsWhenCodeRestrictedToOtherPlansOrDurations(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("planrestrict-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	otherPlanID := seedPlan(t, pool, ctx, pid+"-other", 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")

	promoID := seedPromotion(t, pool, ctx, "PLANONLY-"+pid, "fixed", 100, 0, nil, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if _, err := pool.Exec(ctx,
		`UPDATE promotion_codes SET plan_ids = $2 WHERE id = $1`,
		promoID, []string{otherPlanID},
	); err != nil {
		t.Fatalf("restrict promotion to other plan: %v", err)
	}

	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)
	_, err := svc.Reserve(ctx, "PLANONLY-"+pid, userID, orderID, planID, 30, 1000, true)
	assertRejectReason(t, err, services.RejectPlanNotEligible)
}

func TestReserve_RejectsNotFirstPurchaseWhenRestricted(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("firstonly-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")
	code := "FIRSTONLY-" + pid
	seedPromotion(t, pool, ctx, code, "fixed", 100, 0, nil, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE promotion_codes SET first_purchase_only = true WHERE code = $1`, code); err != nil {
		t.Fatalf("set first_purchase_only: %v", err)
	}
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	_, err := svc.Reserve(ctx, code, userID, orderID, planID, 30, 1000, false)
	assertRejectReason(t, err, services.RejectNotFirstPurchase)
}

func TestRelease_ReturnsTheSlotAndIsIdempotent(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("release-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	userID := seedUser(t, pool, ctx, pid+"@test.com", planID, nil, nil, nil, 0, "active")
	capacity := 1
	promoID := seedPromotion(t, pool, ctx, "RELEASEME-"+pid, "fixed", 100, 0, &capacity, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	if _, err := svc.Reserve(ctx, "RELEASEME-"+pid, userID, orderID, planID, 30, 1000, true); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got := redeemedCount(t, pool, ctx, promoID); got != 1 {
		t.Fatalf("expected redeemed_count 1 before release, got %d", got)
	}

	if err := svc.Release(ctx, orderID); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if got := redeemedCount(t, pool, ctx, promoID); got != 0 {
		t.Errorf("expected redeemed_count 0 after release, got %d", got)
	}
	if got := redemptionStatus(t, pool, ctx, orderID); got != "released" {
		t.Errorf("expected redemption status released, got %s", got)
	}

	// A second Release for the same order - the Cancel handler and the
	// expiry sweep can both call it - must not decrement redeemed_count a
	// second time, since the slot was already returned.
	if err := svc.Release(ctx, orderID); err != nil {
		t.Fatalf("second Release should be a no-op, not an error: %v", err)
	}
	if got := redeemedCount(t, pool, ctx, promoID); got != 0 {
		t.Errorf("expected redeemed_count to stay 0 after a repeated release, got %d", got)
	}

	// The freed slot must be immediately reusable by another order. A second
	// pending order for the same user would collide with
	// idx_plan_orders_one_pending, so the first is cancelled before seeding it -
	// Reserve itself does not care about the order's status.
	if _, err := pool.Exec(ctx, `UPDATE plan_orders SET status = 'cancelled' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("cancel first order: %v", err)
	}
	order2 := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)
	if _, err := svc.Reserve(ctx, "RELEASEME-"+pid, userID, order2, planID, 30, 1000, true); err != nil {
		t.Errorf("expected the released slot to be reusable, got: %v", err)
	}
}

// TestReserve_ConcurrentRedemptionsCannotExceedTheOverallCap is the exit
// criterion's own concurrent-redemption case: N goroutines race to redeem a
// code capped at fewer than N slots, and exactly the cap's worth must
// succeed - never more, regardless of how the requests interleave.
func TestReserve_ConcurrentRedemptionsCannotExceedTheOverallCap(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPromotionService(pool)

	pid := fmt.Sprintf("race-%d", os.Getpid())
	planID := seedPlan(t, pool, ctx, pid, 30, nil)
	const capacity = 3
	const attempts = 10
	capPtr := capacity
	promoID := seedPromotion(t, pool, ctx, "RACE-"+pid, "fixed", 100, 0, &capPtr, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	type outcome struct {
		ok  bool
		err error
	}
	results := make([]outcome, attempts)
	orderIDs := make([]string, attempts)
	userIDs := make([]string, attempts)
	for i := 0; i < attempts; i++ {
		userIDs[i] = seedUser(t, pool, ctx, fmt.Sprintf("%s-%d@test.com", pid, i), planID, nil, nil, nil, 0, "active")
		orderIDs[i] = seedOrder(t, pool, ctx, userIDs[i], planID, 30, 1000, "pending", nil)
	}

	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Reserve(ctx, "RACE-"+pid, userIDs[i], orderIDs[i], planID, 30, 1000, true)
			results[i] = outcome{ok: err == nil, err: err}
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, r := range results {
		if r.ok {
			succeeded++
			continue
		}
		var reject *services.PromotionRejectError
		if !errors.As(r.err, &reject) || reject.Reason != services.RejectOverallCapReached {
			t.Errorf("unexpected error from a failed concurrent Reserve: %v", r.err)
		}
	}

	if succeeded != capacity {
		t.Errorf("expected exactly %d successful concurrent redemptions, got %d", capacity, succeeded)
	}
	if got := redeemedCount(t, pool, ctx, promoID); got != capacity {
		t.Errorf("expected redeemed_count to settle at %d, got %d", capacity, got)
	}
}

func assertRejectReason(t *testing.T, err error, want services.PromotionRejectReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a PromotionRejectError with reason %s, got nil error", want)
	}
	var reject *services.PromotionRejectError
	if !errors.As(err, &reject) {
		t.Fatalf("expected a PromotionRejectError, got %T: %v", err, err)
	}
	if reject.Reason != want {
		t.Errorf("expected reject reason %s, got %s", want, reject.Reason)
	}
}
