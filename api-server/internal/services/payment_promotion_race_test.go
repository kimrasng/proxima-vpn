package services_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestSettle_PromotionZeroAmountRejectsNonzeroOrder(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("promotion-zero-guard-%d", time.Now().UnixNano())
	planID := seedPlan(t, pool, ctx, suffix, 30, nil)
	userID := seedUser(t, pool, ctx, suffix+"@x.test", planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_events WHERE order_id = $1`, orderID)
	})

	result, err := services.NewPaymentService(pool).Settle(ctx, payments.Confirmation{
		Provider: "promotion", ExternalID: suffix, OrderID: orderID, AmountCents: 0,
		ActorType: "user", ActorID: userID,
	})
	if err != nil || result == nil || result.Outcome != services.SettleIgnored || result.Reason != "amount_mismatch" || result.Grant != nil {
		t.Fatalf("zero-amount promotion settlement = %+v, err=%v; want ignored/amount_mismatch without grant", result, err)
	}
	if got := orderStatus(t, pool, ctx, orderID); got != "pending" {
		t.Fatalf("nonzero order status = %q, want pending", got)
	}
	var expires *time.Time
	var paidAt *time.Time
	var price int64
	if err := pool.QueryRow(ctx,
		`SELECT u.plan_expires_at, o.paid_at, o.price_cents
		 FROM users u JOIN plan_orders o ON o.user_id = u.id WHERE o.id = $1`, orderID,
	).Scan(&expires, &paidAt, &price); err != nil {
		t.Fatal(err)
	}
	if expires != nil || paidAt != nil || price != 1000 {
		t.Fatalf("nonzero order after zero settlement: expiry=%v paid_at=%v price=%d; want nil/nil/1000", expires, paidAt, price)
	}
	if outcome, count := eventOutcome(t, pool, ctx, "promotion", suffix); outcome != "ignored" || count != 1 {
		t.Fatalf("zero-amount event outcome/count = %q/%d, want ignored/1", outcome, count)
	}
}
