package services_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// seedOrder inserts a plan_orders row in the given status, cleaning it up on
// test completion. expiresAt nil means no deadline (matches a pre-Phase-6
// row); pass a time to simulate a pending order approaching or past its TTL.
func seedOrder(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID, planID string, durationDays int, priceCents int64, status string, expiresAt *time.Time) string {
	t.Helper()

	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`,
		userID, planID, durationDays, priceCents, status, expiresAt,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM plan_orders WHERE id = $1`, orderID) })
	return orderID
}

func orderStatus(t *testing.T, pool *pgxpool.Pool, ctx context.Context, orderID string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM plan_orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order status: %v", err)
	}
	return status
}

func eventOutcome(t *testing.T, pool *pgxpool.Pool, ctx context.Context, provider, externalID string) (string, int) {
	t.Helper()
	var outcome string
	var count int
	err := pool.QueryRow(ctx,
		`SELECT outcome, (SELECT count(*) FROM payment_events WHERE provider = $1 AND external_id = $2)
		 FROM payment_events WHERE provider = $1 AND external_id = $2`,
		provider, externalID,
	).Scan(&outcome, &count)
	if err != nil {
		t.Fatalf("read event outcome: %v", err)
	}
	return outcome, count
}

// A first-time confirmation for a pending order must grant the plan exactly
// once, using the order's own duration - not the plan's default - and record
// the event as the mechanism that made this settlement possible to detect a
// replay of.
func TestSettle_GrantsOncePerOrder(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("settle-fresh-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("settle-fresh-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 90, 2700, "pending", nil)

	externalID := fmt.Sprintf("evt_grant_once_%d", os.Getpid())
	res, err := svc.Settle(ctx, payments.Confirmation{
		Provider:   "stripe",
		ExternalID: externalID,
		OrderID:    orderID,
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleGranted {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, services.SettleGranted)
	}
	if res.Grant == nil || res.Grant.Kind != services.GrantFresh {
		t.Errorf("Grant.Kind = %v, want %q", res.Grant, services.GrantFresh)
	}

	if status := orderStatus(t, pool, ctx, orderID); status != "paid" {
		t.Errorf("order status = %q, want paid", status)
	}
	outcome, count := eventOutcome(t, pool, ctx, "stripe", externalID)
	if outcome != "granted" || count != 1 {
		t.Errorf("event outcome/count = %q/%d, want granted/1", outcome, count)
	}
}

func TestSettle_ConfirmsPromotionReservation(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	paymentService := services.NewPaymentService(pool)
	promotionService := services.NewPromotionService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("settle-promo-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("settle-promo-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)
	now := time.Now()
	seedPromotion(t, pool, ctx, fmt.Sprintf("SETTLEPROMO-%d", os.Getpid()), "fixed", 100, 0, nil, 1,
		now.Add(-time.Hour), now.Add(time.Hour))
	if _, err := promotionService.Reserve(ctx, fmt.Sprintf("SETTLEPROMO-%d", os.Getpid()), userID, orderID, planID, 30, 1000, true); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	res, err := paymentService.Settle(ctx, payments.Confirmation{
		Provider:   "stripe",
		ExternalID: fmt.Sprintf("evt_promo_confirm_%d", os.Getpid()),
		OrderID:    orderID,
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleGranted {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, services.SettleGranted)
	}
	if status := redemptionStatus(t, pool, ctx, orderID); status != "confirmed" {
		t.Errorf("promotion redemption status = %q, want confirmed", status)
	}
}

// The exact same event replayed - a Stripe retry, or a captured payload
// resent later - must not grant twice. This is the "a replay cannot grant
// twice" exit criterion: the second call answers SettleDuplicate and never
// touches plan_orders at all, so the anchor test that follows can assert
// plan_expires_at moved exactly once.
func TestSettle_DuplicateEventDoesNotGrantTwice(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("settle-dup-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("settle-dup-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	conf := payments.Confirmation{Provider: "stripe", ExternalID: fmt.Sprintf("evt_dup_1_%d", os.Getpid()), OrderID: orderID}

	first, err := svc.Settle(ctx, conf)
	if err != nil {
		t.Fatalf("first Settle: %v", err)
	}
	if first.Outcome != services.SettleGranted {
		t.Fatalf("first Outcome = %q, want granted", first.Outcome)
	}

	var expiresAfterFirst time.Time
	if err := pool.QueryRow(ctx, `SELECT plan_expires_at FROM users WHERE id = $1`, userID).Scan(&expiresAfterFirst); err != nil {
		t.Fatalf("read plan_expires_at: %v", err)
	}

	second, err := svc.Settle(ctx, conf)
	if err != nil {
		t.Fatalf("second Settle: %v", err)
	}
	if second.Outcome != services.SettleDuplicate {
		t.Fatalf("second Outcome = %q, want duplicate", second.Outcome)
	}

	var expiresAfterSecond time.Time
	if err := pool.QueryRow(ctx, `SELECT plan_expires_at FROM users WHERE id = $1`, userID).Scan(&expiresAfterSecond); err != nil {
		t.Fatalf("read plan_expires_at: %v", err)
	}
	if !expiresAfterFirst.Equal(expiresAfterSecond) {
		t.Errorf("plan_expires_at moved on replay: %v -> %v", expiresAfterFirst, expiresAfterSecond)
	}

	_, count := eventOutcome(t, pool, ctx, "stripe", conf.ExternalID)
	if count != 1 {
		t.Errorf("payment_events row count = %d, want exactly 1 (no duplicate row inserted)", count)
	}
}

// Two distinct events aimed at the same order (e.g. Stripe's sync and async
// success events for one session) are the second, independent guard:
// payment_events would admit both since their external ids differ, so the
// order-claim's status filter is what stops the second from granting again.
func TestSettle_SecondDistinctEventOnPaidOrderIsIgnored(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("settle-second-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("settle-second-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	if _, err := svc.Settle(ctx, payments.Confirmation{Provider: "stripe", ExternalID: fmt.Sprintf("evt_A_%d", os.Getpid()), OrderID: orderID}); err != nil {
		t.Fatalf("first Settle: %v", err)
	}

	evtB := fmt.Sprintf("evt_B_%d", os.Getpid())
	res, err := svc.Settle(ctx, payments.Confirmation{Provider: "stripe", ExternalID: evtB, OrderID: orderID})
	if err != nil {
		t.Fatalf("second Settle: %v", err)
	}
	if res.Outcome != services.SettleIgnored {
		t.Fatalf("Outcome = %q, want ignored", res.Outcome)
	}

	outcome, _ := eventOutcome(t, pool, ctx, "stripe", evtB)
	if outcome != "ignored" {
		t.Errorf("evt_B outcome = %q, want ignored", outcome)
	}
}

// A confirmed amount that does not match the order's own snapshotted price
// must not grant - this is the check against a misconfigured price or a
// tampered relay, since the client cannot forge the order's own row.
func TestSettle_AmountMismatchIsIgnored(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("settle-amt-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("settle-amt-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	res, err := svc.Settle(ctx, payments.Confirmation{
		Provider:    "stripe",
		ExternalID:  fmt.Sprintf("evt_amt_wrong_%d", os.Getpid()),
		OrderID:     orderID,
		AmountCents: 500, // order snapshot is 1000
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleIgnored || res.Reason != "amount_mismatch" {
		t.Fatalf("Outcome/Reason = %q/%q, want ignored/amount_mismatch", res.Outcome, res.Reason)
	}
	// The order claim must have been reverted, not left half-paid.
	if status := orderStatus(t, pool, ctx, orderID); status != "pending" {
		t.Errorf("order status after amount mismatch = %q, want reverted to pending", status)
	}
}

// A cancelled order receiving a late payment must not be granted - a cancel
// is a refusal, unlike an expiry which is only a timeout.
func TestSettle_CancelledOrderRejectsLatePayment(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("settle-cancelled-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("settle-cancelled-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "cancelled", nil)

	res, err := svc.Settle(ctx, payments.Confirmation{Provider: "stripe", ExternalID: fmt.Sprintf("evt_late_cancel_%d", os.Getpid()), OrderID: orderID})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleIgnored {
		t.Fatalf("Outcome = %q, want ignored", res.Outcome)
	}
	if status := orderStatus(t, pool, ctx, orderID); status != "cancelled" {
		t.Errorf("order status = %q, want unchanged cancelled", status)
	}
}

// An expired order receiving a late confirmation IS honoured - this is the
// exact exit criterion. Expiring is only a sweep timeout, not a refusal, and
// GrantPlanWithDuration's own GREATEST(expiry,NOW()) formula already grants
// correctly from NOW() when the old expiry has passed.
func TestSettle_ExpiredOrderHonoursLateConfirmation(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("settle-expired-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("settle-expired-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "expired", nil)

	res, err := svc.Settle(ctx, payments.Confirmation{Provider: "stripe", ExternalID: fmt.Sprintf("evt_late_expired_%d", os.Getpid()), OrderID: orderID})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleGranted {
		t.Fatalf("Outcome = %q, want granted (a late confirmation on an expired order is still honoured)", res.Outcome)
	}
	if status := orderStatus(t, pool, ctx, orderID); status != "paid" {
		t.Errorf("order status = %q, want paid", status)
	}
}

// An order id that resolves to nothing (a webhook whose metadata never
// matched a real order) must still be recorded before being rejected - the
// event insert happens regardless of whether the order exists.
func TestSettle_UnknownOrderIsIgnoredButRecorded(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	externalID := fmt.Sprintf("evt_unknown_order_%d", os.Getpid())
	res, err := svc.Settle(ctx, payments.Confirmation{
		Provider:   "stripe",
		ExternalID: externalID,
		OrderID:    "00000000-0000-0000-0000-000000000000",
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleIgnored {
		t.Fatalf("Outcome = %q, want ignored", res.Outcome)
	}
	outcome, count := eventOutcome(t, pool, ctx, "stripe", externalID)
	if outcome != "ignored" || count != 1 {
		t.Errorf("event outcome/count = %q/%d, want ignored/1", outcome, count)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM payment_events WHERE provider = 'stripe' AND external_id = $1`, externalID)
}
