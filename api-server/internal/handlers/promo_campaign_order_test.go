package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestPromoCampaignEnforcesAccountDomainAndReturnsName(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	_, code := seedPartner(t, pool, true)
	orderID := seedPendingOrder(t, pool, userID, planID)
	if _, err := pool.Exec(ctx, `INSERT INTO promo_landing_pages(code,headline,email_domains)
		VALUES($1,'Welcome campaign',ARRAY['example.test'])`, code); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM promo_landing_pages WHERE code=$1`, code) })
	h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}
	resp := orderResponse{ID: orderID}
	app := fiber.New()
	app.Get("/promo/:code", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return NewPromoLandingHandler(pool).Get(c)
	})
	preview, err := app.Test(httptest.NewRequest("GET", "/promo/"+code, nil))
	if err != nil {
		t.Fatal(err)
	}
	var landing promoLandingResponse
	if err := json.NewDecoder(preview.Body).Decode(&landing); err != nil {
		t.Fatal(err)
	}
	_ = preview.Body.Close()
	if preview.StatusCode != 200 || landing.IsValid {
		t.Fatalf("signed-in account domain was not enforced during lookup: %+v", landing)
	}
	if reason := rejectReason(h.applyPromotion(ctx, &resp, userID, code)); reason != "not_eligible" {
		t.Fatalf("domain mismatch reason=%q", reason)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, userID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := h.applyPromotion(ctx, &resp, userID, code); err != nil {
		t.Fatal(err)
	}
	if resp.PromotionName != "Welcome campaign" || resp.PromotionCode != code || resp.PriceCents != 800 {
		t.Fatalf("named promotion=%+v", resp)
	}
}

func TestPromoCampaignLandingIncludesFeaturesAndEligiblePrices(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	_, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	if _, err := pool.Exec(ctx, `INSERT INTO plan_prices(plan_id,duration_days,price_cents)
		VALUES($1,90,2700)`, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_features(plan_id,position,included)
		VALUES($1,0,true),($1,1,false)`, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_feature_texts(plan_id,position,language,text)
		VALUES($1,0,'ko','included-feature'),($1,1,'ko','excluded-feature')`, planID); err != nil {
		t.Fatal(err)
	}
	handler := NewPromoLandingHandler(pool)
	trial, err := handler.landingPlan(ctx, &planID, nil, []string{"30"}, "ko", 0)
	if err != nil {
		t.Fatal(err)
	}
	if trial == nil || len(trial.Prices) != 1 || trial.Prices[0].DurationDays != 30 ||
		len(trial.Features) != 2 || !trial.Features[0].Included || trial.Features[1].Included {
		t.Fatalf("trial omitted features or exposed unrelated durations: %+v", trial)
	}
	discount, err := handler.landingPlan(ctx, &planID, nil, nil, "ko", 2000)
	if err != nil {
		t.Fatal(err)
	}
	if discount == nil || discount.DurationDays != 90 || discount.PriceCents != 2700 ||
		len(discount.Prices) != 1 || discount.Prices[0].DurationDays != 90 {
		t.Fatalf("offer advertised a duration below minimum order: %+v", discount)
	}
}

func TestCardTrialCheckoutReusesNormalOrder(t *testing.T) {
	for _, orderStatus := range []string{"pending", "expired"} {
		t.Run(orderStatus, func(t *testing.T) {
			pool := checkoutKindsDB(t)
			ctx := context.Background()
			userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
			code := seedTrial(t, pool, planID, nil, 1, true)
			orderID := seedPendingOrder(t, pool, userID, planID)
			t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID) })
			h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}
			resp := orderResponse{ID: orderID}
			if err := h.applyPromotion(ctx, &resp, userID, code); err != nil {
				t.Fatal(err)
			}
			gateway := &trialGatewayTest{}
			service := services.NewStripeTrialService(pool, gateway)
			if _, err := service.StartForOrder(ctx, userID, code, orderID); err != nil {
				t.Fatal(err)
			}
			var linked string
			if err := pool.QueryRow(ctx, `SELECT trial_order_id::text FROM stripe_trial_subscriptions WHERE user_id=$1`, userID).Scan(&linked); err != nil {
				t.Fatal(err)
			}
			if linked != orderID {
				t.Fatalf("linked order=%s, want %s", linked, orderID)
			}
			if _, err := service.StartForOrder(ctx, userID, code, orderID); err != nil {
				t.Fatalf("resume own order: %v", err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM plan_orders WHERE user_id=$1`, userID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("checkout created %d orders", count)
			}
			end := time.Date(2030, 1, 8, 12, 0, 0, 0, time.UTC).Unix()
			gateway.state = &payments.TrialSubscriptionState{
				ID: "sub_" + gateway.request.ID, LocalID: gateway.request.ID, CustomerID: "cus_normal",
				Status: "trialing", HasCard: true, PriceCents: 1000, Currency: "usd",
				TrialStart: end - 7*86400, TrialEnd: end, PeriodEnd: end, DurationDays: 30, Quantity: 1,
			}
			event := payments.TrialEvent{
				ID: "evt_" + gateway.request.ID, Kind: "checkout.session.completed", LocalID: gateway.request.ID,
				SessionID: "cs_" + gateway.request.ID, SubscriptionID: gateway.state.ID, CustomerID: "cus_normal", Email: gateway.request.UserEmail,
			}
			if orderStatus == "expired" {
				if _, err := pool.Exec(ctx, `UPDATE plan_orders SET status='expired',
			expires_at=TIMESTAMPTZ '2020-01-01 00:00:00Z' WHERE id=$1`, orderID); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := service.Complete(ctx, event); err != nil {
					t.Fatal(err)
				}
			}
			var status string
			var days int
			if err := pool.QueryRow(ctx, `SELECT status,duration_days FROM plan_orders WHERE id=$1`, orderID).Scan(&status, &days); err != nil {
				t.Fatal(err)
			}
			if status != "paid" || days != 7 {
				t.Fatalf("completed order status=%s days=%d", status, days)
			}
		})
	}
}

func TestCardTrialCheckoutReplacesWebhookExpiredOrderLink(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := seedTrial(t, pool, planID, nil, 1, true)
	orderID := seedPendingOrder(t, pool, userID, planID)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID) })
	h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}
	resp := orderResponse{ID: orderID}
	if err := h.applyPromotion(ctx, &resp, userID, code); err != nil {
		t.Fatal(err)
	}
	gateway := &trialGatewayTest{}
	service := services.NewStripeTrialService(pool, gateway)
	first, err := service.StartForOrder(ctx, userID, code, orderID)
	if err != nil {
		t.Fatal(err)
	}
	oldID := gateway.request.ID
	if _, err := service.Process(ctx, payments.TrialEvent{
		Kind: "checkout.session.expired", LocalID: oldID, SessionID: first.SessionID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartForOrder(ctx, userID, code, orderID); err != nil {
		t.Fatalf("webhook-first expiry stranded normal order: %v", err)
	}
	if gateway.request.ID == oldID {
		t.Fatal("replacement reused expired checkout claim")
	}
	var linked string
	if err := pool.QueryRow(ctx, `SELECT trial_order_id::text FROM stripe_trial_subscriptions WHERE id=$1`,
		gateway.request.ID).Scan(&linked); err != nil || linked != orderID {
		t.Fatalf("replacement order link=%s err=%v", linked, err)
	}
}
