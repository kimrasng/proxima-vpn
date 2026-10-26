package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// fakeConfirmer stands in for Stripe: it answers ConfirmSession from a fixed
// table, so the test exercises the real handler, Settle and plan grant.
type fakeConfirmer struct {
	sessions map[string]*payments.Confirmation
}

func (f *fakeConfirmer) Name() string { return payments.ProviderStripe }

func (f *fakeConfirmer) StartCheckout(context.Context, payments.CheckoutRequest) (*payments.CheckoutHandoff, error) {
	return &payments.CheckoutHandoff{Mode: payments.ModeRedirect}, nil
}

func (f *fakeConfirmer) ConfirmSession(_ context.Context, sessionID string) (*payments.Confirmation, error) {
	conf, ok := f.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("no such session %s", sessionID)
	}
	if conf == nil {
		return nil, nil
	}
	copied := *conf
	return &copied, nil
}

func TestConfirmCheckout_SettlesPaidSessionOnce(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("confirm-%d", time.Now().UnixNano())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_events WHERE order_id IN (SELECT id FROM plan_orders WHERE user_id = $1)`, userID)
	})

	otherUserID, otherPlanID, _ := seedFreeOrderData(t, pool, ctx, suffix+"-b")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_events WHERE order_id IN (SELECT id FROM plan_orders WHERE user_id = $1)`, otherUserID)
	})

	// The schema allows one pending order per user, so each order is seeded only
	// once the previous one for that user has been settled.
	seedOrder := func(owner, plan string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status, provider, expires_at)
			 VALUES ($1, $2, 30, 1000, 'pending', 'stripe', NOW() + INTERVAL '1 hour') RETURNING id::text`,
			owner, plan,
		).Scan(&id); err != nil {
			t.Fatalf("seed order: %v", err)
		}
		return id
	}
	paidOrder := seedOrder(userID, planID)
	otherOrder := seedOrder(otherUserID, otherPlanID)
	paidConf := func(orderID, session string) *payments.Confirmation {
		return &payments.Confirmation{
			Provider: payments.ProviderStripe, ExternalID: "session:" + session, OrderID: orderID,
			AmountCents: 1000, Currency: "usd", SessionID: session,
			ActorType: "provider", ActorID: payments.ProviderStripe, ActorLabel: "buyer@x.test",
		}
	}
	fake := &fakeConfirmer{sessions: map[string]*payments.Confirmation{
		"cs_paid":    paidConf(paidOrder, "cs_paid"),
		"cs_unpaid":  nil,
		"cs_foreign": paidConf(otherOrder, "cs_foreign"),
	}}
	handler := NewUserCheckoutHandler(pool, payments.Registry{payments.ProviderStripe: fake})
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", c.Get("X-Test-User", userID))
		return c.Next()
	})
	app.Post("/orders/:id/checkout/confirm", handler.Confirm)

	confirm := func(orderID, session, asUser string) (int, string) {
		body, _ := json.Marshal(map[string]string{"session_id": session})
		req := httptest.NewRequest("POST", "/orders/"+orderID+"/checkout/confirm", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if asUser != "" {
			req.Header.Set("X-Test-User", asUser)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("confirm request: %v", err)
		}
		var out map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out["status"]
	}
	orderStatus := func(orderID string) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM plan_orders WHERE id = $1`, orderID).Scan(&s); err != nil {
			t.Fatalf("read order: %v", err)
		}
		return s
	}

	if code, status := confirm(paidOrder, "cs_paid", ""); code != 200 || status != "paid" {
		t.Fatalf("paid session: got %d %q, want 200 paid", code, status)
	}
	var grantedPlan *string
	if err := pool.QueryRow(ctx, `SELECT plan_id::text FROM users WHERE id = $1`, userID).Scan(&grantedPlan); err != nil {
		t.Fatalf("read user plan: %v", err)
	}
	if grantedPlan == nil || *grantedPlan != planID {
		t.Fatalf("plan not granted: got %v want %s", grantedPlan, planID)
	}
	var expiresBefore time.Time
	_ = pool.QueryRow(ctx, `SELECT plan_expires_at FROM users WHERE id = $1`, userID).Scan(&expiresBefore)

	if code, status := confirm(paidOrder, "cs_paid", ""); code != 200 || status != "paid" {
		t.Fatalf("repeat confirm: got %d %q, want 200 paid", code, status)
	}
	var expiresAfter time.Time
	_ = pool.QueryRow(ctx, `SELECT plan_expires_at FROM users WHERE id = $1`, userID).Scan(&expiresAfter)
	if !expiresAfter.Equal(expiresBefore) {
		t.Fatalf("repeat confirm extended the plan: %v -> %v", expiresBefore, expiresAfter)
	}

	unpaidOrder := seedOrder(userID, planID)
	if code, status := confirm(unpaidOrder, "cs_unpaid", ""); code != 200 || status != "pending" {
		t.Fatalf("unpaid session: got %d %q, want 200 pending", code, status)
	}
	if code, _ := confirm(unpaidOrder, "cs_foreign", ""); code != 400 {
		t.Fatalf("session for another order: got %d, want 400", code)
	}
	if got := orderStatus(otherOrder); got != "pending" {
		t.Fatalf("foreign session settled the wrong order: %s", got)
	}
	if code, _ := confirm(unpaidOrder, "cs_paid", otherUserID); code != 404 {
		t.Fatalf("another user's order: got %d, want 404", code)
	}
}

func TestReceiptProviderName(t *testing.T) {
	cases := []struct{ provider, session, want string }{
		{"free_trial", "cs_test_123", "stripe"},
		{"free_trial", "SPRING24", "free_trial"},
		{"stripe", "cs_test_123", "stripe"},
		{"manual", "", "manual"},
	}
	for _, tc := range cases {
		if got := receiptProviderName(tc.provider, tc.session); got != tc.want {
			t.Errorf("receiptProviderName(%q, %q) = %q, want %q", tc.provider, tc.session, got, tc.want)
		}
	}
}
