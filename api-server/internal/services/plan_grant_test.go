package services_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestPlanGrant_RecordsPaidOrderAndExtendsTerm(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("grant-%d", time.Now().UnixNano())
	limit := int64(1000)
	planID := seedPlan(t, pool, ctx, suffix, 30, &limit)
	start := time.Now().Add(-5 * 24 * time.Hour)
	expires := time.Now().Add(10 * 24 * time.Hour)
	userID := seedUser(t, pool, ctx, suffix+"@x.test", planID, &start, &expires, nil, 400, "active")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_events WHERE order_id IN (SELECT id FROM plan_orders WHERE user_id = $1)`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plan_orders WHERE user_id = $1`, userID)
	})
	svc := services.NewPlanGrantService(pool)

	orderID, res, err := svc.Grant(ctx, services.PlanGrantRequest{
		UserID: userID, PlanID: planID, DurationDays: 90, PriceCents: 0, Note: "event", ActorLabel: "admin@x.test",
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if res.Grant == nil || res.Grant.Kind != services.GrantRenewal {
		t.Fatalf("grant result = %+v, want a renewal of the running term", res)
	}
	if want := expires.Add(90 * 24 * time.Hour); res.Grant.PlanExpiresAt.Sub(want).Abs() > time.Minute {
		t.Errorf("expiry = %v, want about %v (90 days on top of the current term)", res.Grant.PlanExpiresAt, want)
	}

	var status, provider, note, paidBy string
	var before, after *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, provider, provider_session_id, paid_by, granted_plan_expires_before, granted_plan_expires_after
		 FROM plan_orders WHERE id = $1`, orderID,
	).Scan(&status, &provider, &note, &paidBy, &before, &after); err != nil {
		t.Fatal(err)
	}
	if status != "paid" || provider != services.ProviderGrant || note != "event" || paidBy != "admin@x.test" || before == nil || after == nil {
		t.Errorf("order = status %q provider %q note %q paidBy %q before %v after %v", status, provider, note, paidBy, before, after)
	}
}

func TestPlanGrant_RejectsWhilePaymentPending(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("grant-pending-%d", time.Now().UnixNano())
	planID := seedPlan(t, pool, ctx, suffix, 30, nil)
	userID := seedUser(t, pool, ctx, suffix+"@x.test", planID, nil, nil, nil, 0, "pending")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM plan_orders WHERE user_id = $1`, userID) })
	if _, err := pool.Exec(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status, expires_at)
		 VALUES ($1, $2, 30, 500, 'pending', NOW() + INTERVAL '1 hour')`, userID, planID); err != nil {
		t.Fatal(err)
	}
	svc := services.NewPlanGrantService(pool)
	if _, _, err := svc.Grant(ctx, services.PlanGrantRequest{UserID: userID, PlanID: planID, DurationDays: 30}); !errors.Is(err, services.ErrGrantPendingOrder) {
		t.Errorf("err = %v, want ErrGrantPendingOrder", err)
	}
	if _, _, err := svc.Grant(ctx, services.PlanGrantRequest{UserID: userID, PlanID: planID, DurationDays: 0}); !errors.Is(err, services.ErrGrantInvalid) {
		t.Errorf("zero duration err = %v, want ErrGrantInvalid", err)
	}
}
