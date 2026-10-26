package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestCardTrialAndRenewalGrantOncePerInvoice(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := seedTrial(t, pool, planID, nil, 1, true)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM stripe_trial_invoices WHERE subscription_id IN
			(SELECT id FROM stripe_trial_subscriptions WHERE user_id=$1)`, userID); err != nil {
			t.Error(err)
		}
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
	gateway.state = &payments.TrialSubscriptionState{
		ID: "sub_" + gateway.request.ID, LocalID: gateway.request.ID,
		CustomerID: "cus_test", HasCard: true, Status: "trialing",
		TrialEnd: time.Date(2030, 1, 8, 12, 0, 0, 0, time.UTC).Unix(), PriceCents: 1500, Currency: "usd",
	}
	gateway.state.PeriodEnd = gateway.state.TrialEnd
	gateway.state.TrialStart = gateway.state.TrialEnd - 7*86400
	gateway.state.DurationDays, gateway.state.Quantity, gateway.state.ItemID = 30, 1, "si_trial"
	completed := payments.TrialEvent{
		ID: "evt_" + gateway.request.ID, Kind: "checkout.session.completed",
		LocalID: gateway.request.ID, SessionID: handoff.SessionID,
		SubscriptionID: gateway.state.ID, CustomerID: "cus_test", Email: gateway.request.UserEmail,
	}
	for range 2 {
		if err := service.Complete(ctx, completed); err != nil {
			t.Fatal(err)
		}
	}
	// The original completion remains idempotent after the remote trial ends.
	gateway.state.Status = "active"
	gateway.state.HasCard = false
	if err := service.Complete(ctx, completed); err != nil {
		t.Fatalf("completed trial became unrecoverable after conversion: %v", err)
	}
	gateway.state.HasCard = true
	var trialExpiry time.Time
	if err := pool.QueryRow(ctx, `SELECT plan_expires_at FROM users WHERE id=$1`, userID).Scan(&trialExpiry); err != nil {
		t.Fatal(err)
	}
	if !trialExpiry.Equal(time.Unix(gateway.state.TrialEnd, 0)) {
		t.Fatalf("trial was restarted at processing time: got=%s want=%s", trialExpiry, time.Unix(gateway.state.TrialEnd, 0))
	}
	invoice := payments.TrialEvent{
		ID: "evt_invoice_" + gateway.request.ID, Kind: "invoice.paid",
		LocalID: gateway.request.ID, SubscriptionID: gateway.state.ID,
		InvoiceID: "in_" + gateway.request.ID, Status: "paid", AmountCents: 1500, Currency: "usd",
		BillingReason: "subscription_cycle", Quantity: 1, ItemID: "si_trial",
		PeriodStart: gateway.state.TrialEnd, PeriodEnd: gateway.state.TrialEnd + 30*86400,
	}
	// An unrelated shopping-cart order must not block already-collected renewal.
	seedPendingOrder(t, pool, userID, planID)
	for range 2 {
		if handled, err := service.Invoice(ctx, invoice); err != nil || !handled {
			t.Fatalf("renewal: handled=%v error=%v", handled, err)
		}
	}
	gateway.state.DurationDays = 365
	if handled, err := service.Invoice(ctx, invoice); err != nil || !handled {
		t.Fatalf("settled invoice replay depended on mutable recurring terms: %v", err)
	}
	gateway.state.DurationDays = 30
	var paidCount int
	var renewedExpiry time.Time
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM plan_orders WHERE user_id=$1
		AND provider='stripe_subscription' AND status='paid'`, userID).Scan(&paidCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT plan_expires_at FROM users WHERE id=$1`, userID).Scan(&renewedExpiry); err != nil {
		t.Fatal(err)
	}
	if paidCount != 1 || !renewedExpiry.Equal(trialExpiry.AddDate(0, 0, 30)) {
		t.Fatalf("duplicate or missing renewal: orders=%d before=%s after=%s", paidCount, trialExpiry, renewedExpiry)
	}
	for _, change := range []struct {
		name  string
		apply func(*payments.TrialEvent)
	}{
		{"adjustment", func(e *payments.TrialEvent) { e.BillingReason = "manual" }},
		{"quantity", func(e *payments.TrialEvent) { e.Quantity = 2 }},
		{"short_period", func(e *payments.TrialEvent) { e.PeriodEnd-- }},
		{"proration", func(e *payments.TrialEvent) { e.Proration = true }},
		{"overlapping_invoice", func(e *payments.TrialEvent) {}},
	} {
		t.Run(change.name, func(t *testing.T) {
			invalid := invoice
			invalid.InvoiceID += "_" + change.name
			change.apply(&invalid)
			if _, err := service.Invoice(ctx, invalid); err == nil {
				t.Fatal("noncontracted invoice granted access")
			}
		})
	}
	if err := service.Cancel(ctx, uuid.NewString()); err != services.ErrTrialSubscriptionNotFound {
		t.Fatalf("another user could address this subscription: %v", err)
	}
	if gateway.canceledID != "" {
		t.Fatal("unauthorized cancellation reached Stripe")
	}
	if err := service.Cancel(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if gateway.canceledID != gateway.state.ID {
		t.Fatalf("cancellation targeted the wrong Stripe subscription: %s", gateway.canceledID)
	}
	// Failed collection must not buy access, and cancellation must not erase it.
	invoice.Kind = "invoice.payment_failed"
	invoice.ID += "_failed"
	if handled, err := service.Invoice(ctx, invoice); err != nil || !handled {
		t.Fatalf("failed payment: handled=%v error=%v", handled, err)
	}
	canceled := payments.TrialEvent{
		ID: "evt_cancel_" + gateway.request.ID, Kind: "customer.subscription.deleted",
		SubscriptionID: gateway.state.ID, Status: "canceled", Created: 200,
	}
	if handled, err := service.ApplyLifecycle(ctx, canceled); err != nil || !handled {
		t.Fatalf("cancel lifecycle: handled=%v error=%v", handled, err)
	}
	stale := canceled
	stale.ID += "_stale"
	stale.Kind, stale.Status, stale.Created = "customer.subscription.updated", "active", 100
	if _, err := service.ApplyLifecycle(ctx, stale); err != nil {
		t.Fatal(err)
	}
	current, err := service.Current(ctx, userID)
	if err != nil || current == nil || current.Status != "canceled" {
		t.Fatalf("stale event reopened canceled subscription: %+v %v", current, err)
	}
	var preservedExpiry time.Time
	if err := pool.QueryRow(ctx, `SELECT plan_expires_at FROM users WHERE id=$1`, userID).Scan(&preservedExpiry); err != nil {
		t.Fatal(err)
	}
	if !preservedExpiry.Equal(renewedExpiry) {
		t.Fatalf("failure or cancellation changed paid access: %s -> %s", renewedExpiry, preservedExpiry)
	}
}
