package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

var errTrialGatewayTest = errors.New("Stripe unavailable")

func (g *trialGatewayTest) TrialCurrency() string { return "usd" }

type trialGatewayTest struct {
	request       payments.TrialCheckoutRequest
	err           error
	state         *payments.TrialSubscriptionState
	canceledID    string
	expiredAbsent bool
}

func (g *trialGatewayTest) StartTrial(_ context.Context, req payments.TrialCheckoutRequest) (*payments.CheckoutHandoff, error) {
	g.request = req
	if g.expiredAbsent && !req.ExpiresAt.After(time.Now()) {
		return nil, payments.ErrTrialCheckoutAbsent
	}
	if g.err != nil {
		return nil, g.err
	}
	return &payments.CheckoutHandoff{Mode: payments.ModeRedirect, SessionID: "cs_" + req.ID, RedirectURL: "https://checkout.stripe.com/test"}, nil
}

func (g *trialGatewayTest) CancelTrial(_ context.Context, id string) error {
	g.canceledID = id
	return nil
}

func (g *trialGatewayTest) ReadTrial(context.Context, string) (*payments.TrialSubscriptionState, error) {
	if g.state == nil {
		return nil, errTrialGatewayTest
	}
	return g.state, nil
}

func (g *trialGatewayTest) ReadTrialCheckout(_ context.Context, id string) (*payments.TrialEvent, error) {
	return &payments.TrialEvent{Kind: "checkout.session.open", SessionID: id,
		LocalID: g.request.ID, CheckoutURL: "https://checkout.stripe.com/test"}, nil
}

func TestStripeTrialStartSnapshotsPriceAndRejectsDuplicate(t *testing.T) {
	// Given
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
	// When
	handoff, err := service.Start(ctx, userID, code)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Mode != payments.ModeRedirect || gateway.request.PriceCents != 1500 ||
		gateway.request.TrialDays != 7 || gateway.request.DurationDays != 30 {
		t.Fatalf("invalid handoff or recurring snapshot: %+v %+v", handoff, gateway.request)
	}
	resumed, err := service.Start(ctx, userID, code)
	if err != nil || resumed.SessionID != handoff.SessionID {
		t.Fatalf("saved checkout did not resume the same session: %+v %v", resumed, err)
	}
}

func TestStripeTrialStartResumesSameClaimAfterGatewayFailure(t *testing.T) {
	// Given
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	maximum := 1
	code := seedTrial(t, pool, planID, &maximum, 1, true)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID); err != nil {
			t.Error(err)
		}
	})
	if _, err := pool.Exec(ctx, `INSERT INTO plan_prices(plan_id,duration_days,price_cents)
		VALUES($1,30,1500) ON CONFLICT(plan_id,duration_days) DO UPDATE SET price_cents=1500`, planID); err != nil {
		t.Fatal(err)
	}
	gateway := &trialGatewayTest{err: errTrialGatewayTest}
	service := services.NewStripeTrialService(pool, gateway)
	// When
	_, err := service.Start(ctx, userID, code)
	// Then
	if !errors.Is(err, errTrialGatewayTest) {
		t.Fatalf("gateway failure lost: %v", err)
	}
	firstID := gateway.request.ID
	gateway.err = nil
	if _, err := service.Start(ctx, userID, code); err != nil {
		t.Fatalf("failed checkout could not resume: %v", err)
	}
	if gateway.request.ID != firstID {
		t.Fatalf("uncertain checkout created a new idempotency key: %s -> %s", firstID, gateway.request.ID)
	}
}

func TestStripeTrialStartReplacesExpiredClaimOnlyAfterRemoteAbsence(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	maximum := 1
	code := seedTrial(t, pool, planID, &maximum, 1, true)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE user_id=$1`, userID); err != nil {
			t.Error(err)
		}
	})
	if _, err := pool.Exec(ctx, `INSERT INTO plan_prices(plan_id,duration_days,price_cents)
		VALUES($1,30,1500) ON CONFLICT(plan_id,duration_days) DO UPDATE SET price_cents=1500`, planID); err != nil {
		t.Fatal(err)
	}
	gateway := &trialGatewayTest{err: errTrialGatewayTest}
	service := services.NewStripeTrialService(pool, gateway)
	if _, err := service.Start(ctx, userID, code); !errors.Is(err, errTrialGatewayTest) {
		t.Fatal(err)
	}
	original := gateway.request.ID
	// Model a crash before sending the request, followed by recovery after
	// the original deadline. Only an authoritative absent result releases it.
	if _, err := pool.Exec(ctx, `UPDATE stripe_trial_subscriptions
		SET checkout_expires_at=TIMESTAMPTZ '2020-01-01 00:00:00Z' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	gateway.err, gateway.expiredAbsent = nil, true
	if _, err := service.Start(ctx, userID, code); err != nil {
		t.Fatalf("uncreated expired session stranded its cap slot: %v", err)
	}
	if gateway.request.ID == original {
		t.Fatal("authoritatively absent expired attempt reused an expired session request")
	}
	var oldStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM stripe_trial_subscriptions WHERE id=$1`, original).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "expired" {
		t.Fatalf("old claim retained a cap slot: %s", oldStatus)
	}
}
