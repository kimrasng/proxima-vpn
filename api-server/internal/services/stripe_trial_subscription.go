package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

var ErrTrialSubscriptionNotFound = errors.New("trial subscription not found")

// ErrRecurringBillingActive means the account is already billed by a Stripe
// subscription, so a one-off plan extension would be charged on top of it.
var ErrRecurringBillingActive = errors.New("recurring billing is active")

// HasRecurringBilling reports whether the user has a Stripe subscription that
// will still charge: it exists in Stripe, is not canceled or expired, and is not
// scheduled to stop. A subscription whose cancellation is already scheduled no
// longer charges, so the user may extend by hand again.
func HasRecurringBilling(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID string) (bool, error) {
	var live bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stripe_trial_subscriptions
		WHERE user_id=$1 AND subscription_id IS NOT NULL AND cancel_at_period_end=false
		AND status IN ('trialing','active','past_due','unpaid','paused'))`, userID).Scan(&live)
	if err != nil {
		return false, fmt.Errorf("check recurring billing: %w", err)
	}
	return live, nil
}

type TrialSubscription struct {
	ID                string     `json:"id"`
	PlanName          string     `json:"plan_name"`
	Code              string     `json:"code"`
	Status            string     `json:"status"`
	PriceCents        int64      `json:"price_cents"`
	Currency          string     `json:"currency"`
	DurationDays      int        `json:"duration_days"`
	TrialEndsAt       *time.Time `json:"trial_ends_at"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	// PaymentMethod is the saved card, including for canceled subscriptions.
	// PaymentMethodUnavailable distinguishes a failed read from no saved card.
	PaymentMethod            *payments.PaymentMethodSummary `json:"payment_method"`
	PaymentMethodUnavailable bool                           `json:"payment_method_unavailable,omitempty"`
}

func (s *StripeTrialService) Current(ctx context.Context, userID string) (*TrialSubscription, error) {
	var result TrialSubscription
	var stripeID *string
	err := s.db.QueryRow(ctx, `SELECT t.id,p.name,f.code,t.status,t.price_cents,t.currency,
		t.duration_days,t.trial_ends_at,t.current_period_end,t.cancel_at_period_end,t.subscription_id
		FROM stripe_trial_subscriptions t JOIN plans p ON p.id=t.plan_id
		JOIN free_trial_promotions f ON f.id=t.promotion_id
		WHERE t.user_id=$1 AND t.status<>'expired'
		ORDER BY t.created_at DESC LIMIT 1`, userID).
		Scan(&result.ID, &result.PlanName, &result.Code, &result.Status, &result.PriceCents, &result.Currency,
			&result.DurationDays, &result.TrialEndsAt, &result.CurrentPeriodEnd, &result.CancelAtPeriodEnd, &stripeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		result.PaymentMethodUnavailable = true
		return &result, fmt.Errorf("read trial subscription: %w", err)
	}
	if result.Status == "checkout" || result.Status == "expired" || stripeID == nil {
		return &result, nil
	}
	if s.gateway == nil {
		result.PaymentMethodUnavailable = true
		return &result, nil
	}
	method, err := s.gateway.ReadPaymentMethod(ctx, *stripeID)
	if err != nil {
		result.PaymentMethodUnavailable = true
		return &result, nil
	}
	result.PaymentMethod = method
	return &result, nil
}

// Cancel only schedules future billing to stop. Existing access is untouched.
// Ownership is resolved locally; callers cannot supply another Stripe ID.
func (s *StripeTrialService) Cancel(ctx context.Context, userID string) error {
	var localID, stripeID string
	var cancelScheduled bool
	err := s.db.QueryRow(ctx, `SELECT id,subscription_id,cancel_at_period_end
		FROM stripe_trial_subscriptions WHERE user_id=$1
		AND status IN ('trialing','active','past_due','unpaid','paused')
		AND subscription_id IS NOT NULL ORDER BY created_at DESC LIMIT 1`, userID).
		Scan(&localID, &stripeID, &cancelScheduled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTrialSubscriptionNotFound
	}
	if err != nil {
		return fmt.Errorf("find subscription to cancel: %w", err)
	}
	if cancelScheduled {
		return nil
	}
	if err := s.gateway.CancelTrial(ctx, stripeID); err != nil {
		return fmt.Errorf("cancel trial billing: %w", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE stripe_trial_subscriptions
		SET cancel_at_period_end=true,updated_at=NOW() WHERE id=$1`, localID); err != nil {
		return fmt.Errorf("save trial cancellation: %w", err)
	}
	return nil
}
