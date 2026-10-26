package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// Only ReadPaymentMethod is expected in Current; other gateway calls fail the test.
type cardReadGateway struct {
	payments.TrialGateway
	method *payments.PaymentMethodSummary
	err    error
	ids    []string
}

func (g *cardReadGateway) ReadPaymentMethod(_ context.Context, id string) (*payments.PaymentMethodSummary, error) {
	g.ids = append(g.ids, id)
	return g.method, g.err
}

func TestStripeTrialCurrentPaymentMethod(t *testing.T) {
	stripeID := "sub_card_fixture"
	card := &payments.PaymentMethodSummary{Brand: "visa", Last4: "4242", ExpMonth: 12, ExpYear: 2030}
	for _, tc := range []struct {
		name        string
		status      string
		stripeID    *string
		method      *payments.PaymentMethodSummary
		readErr     error
		noGateway   bool
		unavailable bool
		wantCalls   int
	}{
		{name: "canceled card", status: "canceled", stripeID: &stripeID, method: card, wantCalls: 1},
		{name: "Stripe read failed", status: "canceled", stripeID: &stripeID, readErr: errors.New("Stripe unavailable"), unavailable: true, wantCalls: 1},
		{name: "provider absent", status: "canceled", stripeID: &stripeID, noGateway: true, unavailable: true},
		{name: "queried no card", status: "canceled", stripeID: &stripeID, wantCalls: 1},
		{name: "checkout not applicable", status: "checkout", stripeID: &stripeID, noGateway: true},
		{name: "no Stripe subscription", status: "canceled", noGateway: true},
		{name: "expired not applicable", status: "expired", stripeID: &stripeID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given an isolated subscription using the existing service DB fixtures.
			pool := testDB(t)
			ctx := context.Background()
			suffix := uuid.NewString()
			planID := seedPlan(t, pool, ctx, suffix, 30, nil)
			userID := seedUser(t, pool, ctx, suffix+"@card.test", planID, nil, nil, nil, 0, "active")
			var promotionID string
			if err := pool.QueryRow(ctx, `INSERT INTO free_trial_promotions
				(code,plan_id,duration_days,trial_days,requires_card)
				VALUES($1,$2,30,7,true) RETURNING id::text`, suffix, planID).Scan(&promotionID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := pool.Exec(ctx, `DELETE FROM free_trial_promotions WHERE id=$1`, promotionID); err != nil {
					t.Error(err)
				}
			})
			var localID string
			if err := pool.QueryRow(ctx, `INSERT INTO stripe_trial_subscriptions
				(user_id,promotion_id,plan_id,status,subscription_id,price_cents,currency,duration_days,trial_days,checkout_expires_at)
				VALUES($1,$2,$3,$4,$5,1500,'usd',30,7,'2030-01-01') RETURNING id::text`,
				userID, promotionID, planID, tc.status, tc.stripeID).Scan(&localID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := pool.Exec(ctx, `DELETE FROM stripe_trial_subscriptions WHERE id=$1`, localID); err != nil {
					t.Error(err)
				}
			})
			gateway := &cardReadGateway{method: tc.method, err: tc.readErr}
			var provider payments.TrialGateway = gateway
			if tc.noGateway {
				provider = nil
			}
			svc := services.NewStripeTrialService(pool, provider)

			// When the billing panel reads its current subscription.
			got, err := svc.Current(ctx, userID)

			// Then failed reads differ from a confirmed absence of a card.
			if err != nil {
				t.Fatal(err)
			}
			if len(gateway.ids) != tc.wantCalls {
				t.Fatalf("card reads = %v, want %d calls; subscription=%+v", gateway.ids, tc.wantCalls, got)
			}
			if tc.status == "expired" {
				if got != nil {
					t.Fatal("expired subscription returned as current")
				}
				return
			}
			if got == nil || got.ID != localID || got.Status != tc.status {
				t.Fatalf("Current returned wrong subscription: %+v", got)
			}
			if got.PaymentMethod != tc.method || got.PaymentMethodUnavailable != tc.unavailable {
				t.Fatalf("card=%+v unavailable=%v, want card=%+v unavailable=%v",
					got.PaymentMethod, got.PaymentMethodUnavailable, tc.method, tc.unavailable)
			}
			if tc.wantCalls == 1 && gateway.ids[0] != stripeID {
				t.Fatalf("read subscription %q, want %q", gateway.ids[0], stripeID)
			}
			body, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			flag, present := payload["payment_method_unavailable"]
			if present != tc.unavailable || (present && string(flag) != "true") {
				t.Fatalf("serialized unavailable = %s (present=%v), want %v", flag, present, tc.unavailable)
			}
		})
	}
}

func TestStripeTrialCurrentMarksDatabaseReadFailure(t *testing.T) {
	// Given a closed pool, which fails without connecting to any database.
	pool, err := pgxpool.New(context.Background(), "postgres://localhost/card_read_test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	gateway := &cardReadGateway{}
	svc := services.NewStripeTrialService(pool, gateway)

	// When the subscription query fails.
	got, err := svc.Current(context.Background(), uuid.NewString())

	// Then it is an explicit error, never a successful no-card response.
	if err == nil || got == nil || !got.PaymentMethodUnavailable || got.PaymentMethod != nil {
		t.Fatalf("Current = %+v, err=%v; want unavailable with DB error", got, err)
	}
	if len(gateway.ids) != 0 {
		t.Fatal("Stripe called after failed subscription query")
	}
}
