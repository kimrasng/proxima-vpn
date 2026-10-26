package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

type billingPortalTestProvider struct {
	customerID string
	calls      int
	err        error
}

func (p *billingPortalTestProvider) Name() string { return payments.ProviderStripe }

func (p *billingPortalTestProvider) StartCheckout(context.Context, payments.CheckoutRequest) (*payments.CheckoutHandoff, error) {
	return nil, errors.New("unexpected checkout")
}

func (p *billingPortalTestProvider) CreateBillingPortal(_ context.Context, customerID string) (string, error) {
	p.customerID = customerID
	p.calls++
	if p.err != nil {
		return "", p.err
	}
	return "https://billing.stripe.com/p/session/fixture", nil
}

func billingPortalRequest(t *testing.T, h *UserTrialSubscriptionHandler, userID string) (int, map[string]string) {
	t.Helper()
	app := fiber.New()
	app.Post("/billing/portal", func(c *fiber.Ctx) error {
		if userID != "" {
			c.Locals("user_id", userID)
		}
		return h.BillingPortal(c)
	})
	req := httptest.NewRequest("POST", "/billing/portal", strings.NewReader(
		`{"customer_id":"cus_foreign","user_id":"foreign","subscription_id":"sub_foreign","return_url":"https://evil.example","redirect_url":"https://evil.example"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestBillingPortalRequiresAuthenticationAndAvailableProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		userID   string
		registry payments.Registry
		status   int
	}{
		{name: "unauthenticated", status: 401},
		{name: "missing Stripe", userID: uuid.NewString(), status: 503},
		{name: "unsupported Stripe", userID: uuid.NewString(), status: 503,
			registry: payments.Registry{payments.ProviderStripe: payments.NewAdminProvider()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given
			h := NewUserTrialSubscriptionHandler(nil, tc.registry)
			// When
			status, body := billingPortalRequest(t, h, tc.userID)
			// Then
			if status != tc.status || body["url"] != "" || len(body) != 1 {
				t.Fatalf("status = %d, body = %v", status, body)
			}
		})
	}
}

func TestBillingPortalUsesLatestOwnedCustomerIncludingCanceled(t *testing.T) {
	// Given
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	owner, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	foreign, _, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	missing, _, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := seedTrial(t, pool, planID, nil, 1, true)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id IN ($1,$2,$3)`, owner, foreign, missing); err != nil {
			t.Error(err)
		}
	})
	oldCustomer, ownedCustomer, foreignCustomer := "cus_old", "cus_owned", "cus_foreign"
	for _, row := range []struct {
		userID   string
		customer *string
		created  string
	}{
		{owner, &oldCustomer, "2025-01-01T00:00:00Z"},
		{owner, &ownedCustomer, "2025-01-02T00:00:00Z"},
		{owner, nil, "2025-01-03T00:00:00Z"},
		{foreign, &foreignCustomer, "2025-01-04T00:00:00Z"},
		{missing, nil, "2025-01-05T00:00:00Z"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO stripe_trial_subscriptions
				(user_id,promotion_id,plan_id,customer_id,price_cents,currency,
				 duration_days,trial_days,status,checkout_expires_at,created_at)
			SELECT $1,id,plan_id,$2,1500,'usd',30,7,'canceled',
				'2025-01-01T01:00:00Z'::timestamptz,$3::text::timestamptz
			FROM free_trial_promotions WHERE code=$4`,
			row.userID, row.customer, row.created, code); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		userID string
		err    error
		status int
		calls  int
	}{
		{name: "latest canceled owned customer", userID: owner, status: 200, calls: 1},
		{name: "no owned customer ignores foreign body", userID: missing, status: 404},
		{name: "Stripe error is sanitized", userID: owner, err: errors.New("sk_test_private_fixture"), status: 502, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &billingPortalTestProvider{err: tc.err}
			h := NewUserTrialSubscriptionHandler(pool, payments.Registry{payments.ProviderStripe: gateway})
			// When
			status, body := billingPortalRequest(t, h, tc.userID)
			// Then
			if status != tc.status || gateway.calls != tc.calls || len(body) != 1 {
				t.Fatalf("status = %d, calls = %d, body = %v", status, gateway.calls, body)
			}
			if tc.calls != 0 && gateway.customerID != "cus_owned" {
				t.Fatalf("selected customer = %q, want latest owned customer", gateway.customerID)
			}
			if status == 200 && body["url"] != "https://billing.stripe.com/p/session/fixture" {
				t.Fatalf("portal URL missing: %v", body)
			}
			if status != 200 && (body["url"] != "" || body["error"] == "" ||
				strings.Contains(body["error"], "sk_test_private_fixture")) {
				t.Fatalf("unsafe error response: %v", body)
			}
		})
	}
}
