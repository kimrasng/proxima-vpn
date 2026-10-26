package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestExtendingThePlanIsRefusedWhileRecurringBillingIsLive(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name           string
		subscription   string // "" = no Stripe subscription row
		status         string
		cancelAtEnd    bool
		wantStatus     int
		wantCodeInBody bool
	}{
		{"no subscription extends normally", "", "", false, fiber.StatusCreated, false},
		{"trialing subscription blocks", "sub_", "trialing", false, fiber.StatusConflict, true},
		{"active subscription blocks", "sub_", "active", false, fiber.StatusConflict, true},
		{"past_due still blocks", "sub_", "past_due", false, fiber.StatusConflict, true},
		{"cancellation already scheduled allows", "sub_", "active", true, fiber.StatusCreated, false},
		{"canceled allows", "sub_", "canceled", false, fiber.StatusCreated, false},
		{"unfinished checkout (no stripe subscription yet) is not recurring billing", "", "checkout", false, fiber.StatusConflict, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
			if _, err := pool.Exec(ctx, `UPDATE users SET plan_id=$2, plan_started_at=NOW(), plan_expires_at=NOW()+INTERVAL '20 days' WHERE id=$1`, userID, planID); err != nil {
				t.Fatal(err)
			}
			if tc.status != "" {
				promotionID := seedTrial(t, pool, planID, nil, 1, true)
				var sub any
				if tc.subscription != "" {
					sub = tc.subscription + uuid.NewString()
				}
				if _, err := pool.Exec(ctx, `INSERT INTO stripe_trial_subscriptions
					(user_id, promotion_id, plan_id, subscription_id, price_cents, currency, duration_days, trial_days,
					 status, cancel_at_period_end, checkout_expires_at)
					SELECT $1, f.id, $2, $3, 1000, 'usd', 30, 7, $4, $5, NOW()+INTERVAL '1 hour'
					FROM free_trial_promotions f WHERE f.code=$6`, userID, planID, sub, tc.status, tc.cancelAtEnd, promotionID); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID) })
			}
			body := []byte(fmt.Sprintf(`{"plan_id":%q,"duration_days":30}`, planID))
			order, status := createOrderApp(t, pool, userID, body, nil)
			if status != tc.wantStatus {
				t.Fatalf("status=%d, want %d (order=%+v)", status, tc.wantStatus, order)
			}
			var pending int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM plan_orders WHERE user_id=$1 AND status='pending'`, userID).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if wantPending := map[bool]int{true: 1, false: 0}[tc.wantStatus == fiber.StatusCreated]; pending != wantPending {
				t.Fatalf("pending orders=%d, want %d: a refused extension must not leave an order behind", pending, wantPending)
			}
		})
	}
}

func TestExtendingIsRefusedWithAMachineReadableCode(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_id=$2, plan_started_at=NOW(), plan_expires_at=NOW()+INTERVAL '20 days' WHERE id=$1`, userID, planID); err != nil {
		t.Fatal(err)
	}
	code := seedTrial(t, pool, planID, nil, 1, true)
	if _, err := pool.Exec(ctx, `INSERT INTO stripe_trial_subscriptions
		(user_id, promotion_id, plan_id, subscription_id, price_cents, currency, duration_days, trial_days, status, checkout_expires_at)
		SELECT $1, f.id, $2, $3, 1000, 'usd', 30, 7, 'trialing', NOW()+INTERVAL '1 hour' FROM free_trial_promotions f WHERE f.code=$4`,
		userID, planID, "sub_"+uuid.NewString(), code); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID) })

	handler := NewUserOrderHandler(pool, config.PaymentsConfig{PendingTTLRaw: "1h"}, payments.Registry{})
	app := fiber.New()
	app.Post("/orders", func(c *fiber.Ctx) error { c.Locals("user_id", userID); return handler.Create(c) })
	request := httptest.NewRequest("POST", "/orders", strings.NewReader(fmt.Sprintf(`{"plan_id":%q,"duration_days":30}`, planID)))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusConflict || body["code"] != "recurring_billing_active" {
		t.Fatalf("status=%d body=%v, want 409 with code recurring_billing_active", response.StatusCode, body)
	}
}

func TestSubscriptionResponseCarriesTheSavedCard(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		method    *payments.PaymentMethodSummary
		methodErr error
		status    string
		wantCard  bool
	}{
		{"card on file", &payments.PaymentMethodSummary{Brand: "visa", Last4: "4242", ExpMonth: 8, ExpYear: 2030, Funding: "credit"}, nil, "trialing", true},
		{"no default card", nil, nil, "trialing", false},
		{"stripe unreachable degrades to no card, not an error", nil, errTrialGatewayTest, "trialing", false},
		{"canceled subscription still shows the card that was on file", &payments.PaymentMethodSummary{Brand: "visa", Last4: "4242"}, nil, "canceled", true},
		{"unfinished checkout has no card yet and is not asked", &payments.PaymentMethodSummary{Brand: "visa", Last4: "4242"}, nil, "checkout", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
			code := seedTrial(t, pool, planID, nil, 1, true)
			if _, err := pool.Exec(ctx, `INSERT INTO stripe_trial_subscriptions
				(user_id, promotion_id, plan_id, subscription_id, price_cents, currency, duration_days, trial_days, status, checkout_expires_at)
				SELECT $1, f.id, $2, $3, 1000, 'usd', 30, 7, $4, NOW()+INTERVAL '1 hour' FROM free_trial_promotions f WHERE f.code=$5`,
				userID, planID, "sub_"+uuid.NewString(), tc.status, code); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID) })
			gateway := &trialGatewayTest{method: tc.method, methodErr: tc.methodErr}
			current, err := services.NewStripeTrialService(pool, gateway).Current(ctx, userID)
			if err != nil || current == nil {
				t.Fatalf("current: %+v %v", current, err)
			}
			if got := current.PaymentMethod != nil; got != tc.wantCard {
				t.Fatalf("card present=%v, want %v (%+v)", got, tc.wantCard, current.PaymentMethod)
			}
			if tc.wantCard && (current.PaymentMethod.Last4 != "4242" || current.PaymentMethod.Brand != "visa") {
				t.Fatalf("card=%+v", current.PaymentMethod)
			}
		})
	}
}
