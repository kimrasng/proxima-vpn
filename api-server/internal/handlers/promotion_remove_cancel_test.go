package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestPromotionRemovalRestoresPriceAndPreservesConfirmedUserLimit(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := "REMOVE-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO promotion_codes
		(code,discount_type,discount_value,valid_from,valid_until,max_redemptions_per_user)
		VALUES($1,'percent',100,NOW()-INTERVAL '1 hour',NOW()+INTERVAL '1 day',1)`, code); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_redemptions WHERE user_id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM plan_orders WHERE user_id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_codes WHERE code=$1`, code)
	})
	h := &UserOrderHandler{db: pool, promotion: services.NewPromotionService(pool)}
	orderID := seedPendingOrder(t, pool, userID, planID)
	order := orderResponse{ID: orderID}
	if err := h.applyPromotion(ctx, &order, userID, code); err != nil {
		t.Fatal(err)
	}
	if order.PriceCents != 0 || order.DiscountCents != 1000 {
		t.Fatalf("expected a fully discounted order: %+v", order)
	}
	app := fiber.New()
	actor := uuid.NewString()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", actor); return c.Next() })
	app.Delete("/orders/:id/promotion", h.RemovePromotion)
	app.Get("/promo/:code", NewPromoLandingHandler(pool).Get)
	foreign, err := app.Test(httptest.NewRequest("DELETE", "/orders/"+orderID+"/promotion", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = foreign.Body.Close()
	if foreign.StatusCode != 404 {
		t.Fatalf("foreign removal status=%d", foreign.StatusCode)
	}
	actor = userID
	for range 2 {
		response, err := app.Test(httptest.NewRequest("DELETE", "/orders/"+orderID+"/promotion", nil))
		if err != nil {
			t.Fatal(err)
		}
		order = orderResponse{}
		if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 200 || order.PriceCents != 1000 || order.DiscountCents != 0 || order.PromotionCode != "" {
			t.Fatalf("removal response=%d order=%+v", response.StatusCode, order)
		}
	}
	var reserved int
	if err := pool.QueryRow(ctx, `SELECT redeemed_count FROM promotion_codes WHERE code=$1`, code).Scan(&reserved); err != nil || reserved != 0 {
		t.Fatalf("released capacity=%d err=%v", reserved, err)
	}
	if err := h.applyPromotion(ctx, &order, userID, code); err != nil {
		t.Fatal(err)
	}
	if _, err := services.NewPaymentService(pool).Settle(ctx, payments.Confirmation{
		Provider: payments.ProviderStripe, ExternalID: "evt_zero_" + uuid.NewString(),
		OrderID: orderID, AmountCents: 0, ActorType: "system",
	}); err != nil {
		t.Fatal(err)
	}
	var paid string
	if err := pool.QueryRow(ctx, `SELECT status FROM plan_orders WHERE id=$1`, orderID).Scan(&paid); err != nil || paid != "paid" {
		t.Fatalf("zero-cost settlement status=%s err=%v", paid, err)
	}
	next := orderResponse{ID: seedPendingOrder(t, pool, userID, planID)}
	if reason := rejectReason(h.applyPromotion(ctx, &next, userID, code)); reason != services.RejectUserCapReached {
		t.Fatalf("confirmed coupon was reusable: reason=%s", reason)
	}
	preview, err := app.Test(httptest.NewRequest("GET", "/promo/"+code, nil))
	if err != nil {
		t.Fatal(err)
	}
	var landing promoLandingResponse
	if err := json.NewDecoder(preview.Body).Decode(&landing); err != nil {
		t.Fatal(err)
	}
	_ = preview.Body.Close()
	if landing.IsEligible || landing.IneligibleReason != "user_cap_reached" || landing.Plan == nil {
		t.Fatalf("used coupon eligibility or offer details incorrect: %+v", landing)
	}
}

type promotionCancelGateway struct {
	payments.Provider
	err      error
	sessions []string
}

func (g *promotionCancelGateway) Name() string { return payments.ProviderStripe }
func (g *promotionCancelGateway) ExpireCheckout(_ context.Context, id string) error {
	g.sessions = append(g.sessions, id)
	return g.err
}

func TestCancelOrderExpiresStripeBeforeReleasingOrder(t *testing.T) {
	for _, tc := range []struct {
		name, initial, final string
		remoteErr            error
		status, calls        int
	}{
		{"open", "pending", "cancelled", nil, 200, 1},
		{"completed", "pending", "pending", payments.ErrCheckoutComplete, 409, 1},
		{"unavailable", "pending", "pending", errors.New("unavailable"), 502, 1},
		{"paid", "paid", "paid", nil, 409, 0},
	} {
		for _, admin := range []bool{false, true} {
			role := "user"
			if admin {
				role = "admin"
			}
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				pool := checkoutKindsDB(t)
				ctx := context.Background()
				userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
				orderID := seedPendingOrder(t, pool, userID, planID)
				if _, err := pool.Exec(ctx, `UPDATE plan_orders SET status=$2,provider='stripe',
				provider_session_id='cs_expire_test' WHERE id=$1`, orderID, tc.initial); err != nil {
					t.Fatal(err)
				}
				gateway := &promotionCancelGateway{err: tc.remoteErr}
				h := &UserOrderHandler{db: pool, promotion: services.NewPromotionService(pool),
					activity: services.NewActivityService(pool), providers: payments.Registry{payments.ProviderStripe: gateway}}
				app := fiber.New()
				app.Post("/orders/:id/cancel", func(c *fiber.Ctx) error {
					if admin {
						c.Locals("admin_id", uuid.NewString())
						return NewAdminOrderHandler(pool, h.providers).Cancel(c)
					}
					c.Locals("user_id", userID)
					return h.Cancel(c)
				})
				response, err := app.Test(httptest.NewRequest("POST", "/orders/"+orderID+"/cancel", nil))
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				var status string
				if err := pool.QueryRow(ctx, `SELECT status FROM plan_orders WHERE id=$1`, orderID).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != tc.status || status != tc.final || len(gateway.sessions) != tc.calls {
					t.Fatalf("http=%d status=%s remote=%v", response.StatusCode, status, gateway.sessions)
				}
			})
		}
	}
}
