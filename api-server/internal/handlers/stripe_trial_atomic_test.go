package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestCardTrialGrantRollsBackReceiptWhenUserUpdateFails(t *testing.T) {
	// Given a real database that rejects this user's entitlement update.
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := seedTrial(t, pool, planID, nil, 1, true)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID); err != nil {
			t.Error(err)
		}
	})
	if _, err := pool.Exec(ctx, `INSERT INTO plan_prices(plan_id,duration_days,price_cents)
		VALUES($1,30,1500) ON CONFLICT(plan_id,duration_days) DO UPDATE SET price_cents=1500`, planID); err != nil {
		t.Fatal(err)
	}
	gateway := &trialGatewayTest{}
	service := services.NewStripeTrialService(pool, gateway)
	handoff, err := service.Start(ctx, userID, code)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2030, 1, 8, 12, 0, 0, 0, time.UTC).Unix()
	gateway.state = &payments.TrialSubscriptionState{
		ID: "sub_" + gateway.request.ID, LocalID: gateway.request.ID, CustomerID: "cus_atomic",
		Status: "trialing", HasCard: true, PriceCents: 1500, Currency: "usd",
		TrialStart: end - 7*86400, TrialEnd: end, PeriodEnd: end,
		DurationDays: 30, Quantity: 1, ItemID: "si_atomic",
	}
	event := payments.TrialEvent{
		ID: "evt_" + gateway.request.ID, Kind: "checkout.session.completed",
		LocalID: gateway.request.ID, SessionID: handoff.SessionID,
		SubscriptionID: gateway.state.ID, CustomerID: "cus_atomic", Email: gateway.request.UserEmail,
	}
	name := "qa_trial_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ddl := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.id::text='%s' THEN RAISE EXCEPTION 'injected entitlement write failure';
		END IF; RETURN NEW; END $$;
		CREATE TRIGGER %s BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION %s()`, name, userID, name, name)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	remove := fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON users; DROP FUNCTION IF EXISTS %s()`, name, name)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, remove); err != nil {
			t.Error(err)
		}
	})
	// When the grant fails after the receipt/order has been staged.
	if err := service.Complete(ctx, event); err == nil {
		t.Fatal("injected database failure was ignored")
	}
	// Then no partially paid receipt or subscription binding escapes rollback.
	var partial bool
	if err := pool.QueryRow(ctx, `SELECT t.trial_order_id IS NOT NULL OR t.subscription_id IS NOT NULL
		OR EXISTS(SELECT 1 FROM plan_orders o WHERE o.user_id=t.user_id AND o.provider='free_trial')
		FROM stripe_trial_subscriptions t WHERE t.id=$1`, gateway.request.ID).Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if partial {
		t.Fatal("failed entitlement left a partial receipt or bound subscription")
	}
	if _, err := pool.Exec(ctx, remove); err != nil {
		t.Fatal(err)
	}
	if err := service.Complete(ctx, event); err != nil {
		t.Fatalf("retry after rollback did not recover: %v", err)
	}
}
