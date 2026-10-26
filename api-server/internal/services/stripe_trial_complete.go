package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// Complete grants a trial only after Stripe confirms its card-backed subscription.
// The durable order link lets a webhook retry resume settlement after a crash.
func (s *StripeTrialService) Complete(ctx context.Context, event payments.TrialEvent) error {
	var alreadyGranted bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1
		FROM stripe_trial_subscriptions t JOIN plan_orders o ON o.id=t.trial_order_id
		WHERE t.id::text=$1 AND t.checkout_session_id=$2 AND t.subscription_id=$3
		AND o.status='paid' AND o.granted_plan_expires_after IS NOT NULL)`,
		event.LocalID, event.SessionID, event.SubscriptionID).Scan(&alreadyGranted); err != nil {
		return fmt.Errorf("read completed trial grant: %w", err)
	}
	if alreadyGranted {
		return nil
	}
	state, err := s.gateway.ReadTrial(ctx, event.SubscriptionID)
	if err != nil {
		return fmt.Errorf("confirm trial subscription: %w", err)
	}
	if state.LocalID != event.LocalID || state.ID != event.SubscriptionID ||
		state.CustomerID != event.CustomerID || !state.HasCard ||
		state.TrialEnd <= 0 {
		return fmt.Errorf("trial subscription identity or card state mismatch")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin trial completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, planID, email, sessionID, status, currency string
	var trialDays, durationDays int
	var price int64
	var orderID *string
	err = tx.QueryRow(ctx, `SELECT t.user_id,t.plan_id,u.email,
		COALESCE(t.checkout_session_id,''),t.status,t.currency,t.price_cents,
		t.trial_days,t.duration_days,t.trial_order_id::text
		FROM stripe_trial_subscriptions t JOIN users u ON u.id=t.user_id
		WHERE t.id=$1 FOR UPDATE OF t,u`, event.LocalID).
		Scan(&userID, &planID, &email, &sessionID, &status, &currency, &price, &trialDays, &durationDays, &orderID)
	if err != nil {
		return fmt.Errorf("lock trial completion: %w", err)
	}
	if sessionID != "" && sessionID != event.SessionID ||
		!strings.EqualFold(email, event.Email) || price != state.PriceCents ||
		currency != state.Currency || state.Quantity != 1 || state.DurationDays != int64(durationDays) ||
		state.TrialEnd-state.TrialStart != int64(trialDays)*86400 {
		return fmt.Errorf("trial checkout differs from local snapshot")
	}
	if orderID == nil || status == "checkout" {
		if status != "checkout" {
			return fmt.Errorf("trial checkout is no longer claimable")
		}
		var hasPlan bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE(plan_expires_at>NOW(),false)
			FROM users WHERE id=$1`, userID).Scan(&hasPlan); err != nil {
			return fmt.Errorf("check trial entitlement: %w", err)
		}
		if hasPlan {
			// An administrator may grant a plan while Checkout is open. Keep
			// that paid access and bind the remote subscription so it remains
			// manageable, but prevent another automatic billing cycle.
			if err := s.gateway.CancelTrial(ctx, state.ID); err != nil {
				return fmt.Errorf("stop competing trial billing: %w", err)
			}
			state.CancelAtEnd = true
		}
		if orderID == nil {
			var created string
			if err := tx.QueryRow(ctx, `INSERT INTO plan_orders
			(user_id,plan_id,duration_days,price_cents,provider,provider_session_id,expires_at,free_trial_id)
			SELECT user_id,plan_id,trial_days,0,'free_trial',$2,
				NOW()+INTERVAL '1 day',promotion_id
			FROM stripe_trial_subscriptions WHERE id=$1 RETURNING id::text`,
				event.LocalID, event.SessionID).Scan(&created); err != nil {
				return fmt.Errorf("create card trial receipt order: %w", err)
			}
			orderID = &created
		} else {
			if _, err := tx.Exec(ctx, `UPDATE plan_orders SET duration_days=$2,
				provider='free_trial',provider_session_id=$3 WHERE id=$1 AND status IN ('pending','expired')`,
				*orderID, trialDays, event.SessionID); err != nil {
				return fmt.Errorf("finalize trial receipt order: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE stripe_trial_subscriptions SET
			trial_order_id=$2,subscription_id=$3,customer_id=$4,
			checkout_session_id=$5,status=$9,trial_ends_at=to_timestamp($6),
			current_period_end=to_timestamp($7),cancel_at_period_end=$8,updated_at=NOW()
			WHERE id=$1`, event.LocalID, *orderID, state.ID, state.CustomerID,
			event.SessionID, state.TrialEnd, state.PeriodEnd, state.CancelAtEnd, state.Status); err != nil {
			return fmt.Errorf("bind trial subscription: %w", err)
		}
	}
	trialEnd := time.Unix(state.TrialEnd, 0)
	if err := settleTrialOrderTx(ctx, tx, *orderID, &trialEnd); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit trial completion: %w", err)
	}
	return nil
}
