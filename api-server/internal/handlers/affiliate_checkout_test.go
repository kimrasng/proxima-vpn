package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type affiliateCheckoutCapture struct{ request payments.CheckoutRequest }

func (p *affiliateCheckoutCapture) Name() string { return payments.ProviderStripe }
func (p *affiliateCheckoutCapture) StartCheckout(_ context.Context, req payments.CheckoutRequest) (*payments.CheckoutHandoff, error) {
	p.request = req
	return &payments.CheckoutHandoff{Mode: payments.ModeRedirect, RedirectURL: "https://checkout.stripe.test/affiliate", SessionID: "cs_affiliate"}, nil
}

func TestAffiliateCheckoutPreservesCodeAndDiscountForStripe(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	_, code := seedPartner(t, pool, true)
	orderID := seedPendingOrder(t, pool, userID, planID)
	orderHandler := &UserOrderHandler{db: pool, promotion: services.NewPromotionService(pool), activity: services.NewActivityService(pool)}
	order := orderResponse{ID: orderID}
	if err := orderHandler.applyPromotion(ctx, &order, userID, code); err != nil {
		t.Fatal(err)
	}
	capture := &affiliateCheckoutCapture{}
	handler := NewUserCheckoutHandler(pool, payments.Registry{payments.ProviderStripe: capture})
	app := fiber.New()
	app.Post("/orders/:id/checkout", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return handler.Start(c)
	})
	req := httptest.NewRequest("POST", "/orders/"+orderID+"/checkout", strings.NewReader(`{"provider":"stripe"}`))
	req.Header.Set("Content-Type", "application/json")
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		t.Fatalf("checkout failed: %d", response.StatusCode)
	}
	if capture.request.PromotionCode != code || capture.request.PriceCents != 800 || capture.request.DiscountCents != 200 {
		t.Fatalf("Stripe lost affiliate discount identity: %+v", capture.request)
	}
}
