package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// ApplyLifecycle records subscription state without modifying already-granted
// access. The invoice-paid path alone is responsible for paid plan extensions.
func (s *StripeTrialService) ApplyLifecycle(ctx context.Context, event payments.TrialEvent) (bool, error) {
	switch event.Kind {
	case "customer.subscription.updated", "customer.subscription.deleted":
	default:
		return false, nil
	}
	if event.Kind == "customer.subscription.updated" {
		state, err := s.gateway.ReadTrial(ctx, event.SubscriptionID)
		if err != nil {
			return false, fmt.Errorf("reconcile subscription lifecycle: %w", err)
		}
		event.Status, event.CancelAtEnd = state.Status, state.CancelAtEnd
		event.TrialEnd, event.PeriodEnd = state.TrialEnd, state.PeriodEnd
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin subscription lifecycle: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id, status string
	var lastCreated int64
	err = tx.QueryRow(ctx, `SELECT id,status,last_event_created FROM stripe_trial_subscriptions
		WHERE subscription_id=$1 FOR UPDATE`, event.SubscriptionID).Scan(&id, &status, &lastCreated)
	if errors.Is(err, pgx.ErrNoRows) {
		// A subscription update can precede checkout completion. Ask Stripe to
		// retry our own subscription until the checkout has bound its identity.
		if event.LocalID != "" {
			return false, fmt.Errorf("trial subscription checkout has not been confirmed")
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock subscription lifecycle: %w", err)
	}
	insert, err := tx.Exec(ctx, `INSERT INTO stripe_trial_events(event_id) VALUES($1)
		ON CONFLICT DO NOTHING`, event.ID)
	if err != nil {
		return false, fmt.Errorf("record subscription event: %w", err)
	}
	if insert.RowsAffected() == 0 {
		return true, nil
	}
	if event.Kind == "customer.subscription.deleted" {
		event.Status = "canceled"
	}
	switch event.Status {
	case "trialing", "active", "past_due", "unpaid", "paused", "canceled":
	default:
		return false, fmt.Errorf("unsupported trial subscription status %q", event.Status)
	}
	if status != "canceled" && event.Created >= lastCreated {
		if _, err := tx.Exec(ctx, `UPDATE stripe_trial_subscriptions SET
			status=$2,cancel_at_period_end=$3,last_event_created=$4,
			trial_ends_at=COALESCE(to_timestamp(NULLIF($5::bigint,0)),trial_ends_at),
			current_period_end=COALESCE(to_timestamp(NULLIF($6::bigint,0)),current_period_end),
			updated_at=NOW() WHERE id=$1`,
			id, event.Status, event.CancelAtEnd, event.Created, event.TrialEnd, event.PeriodEnd); err != nil {
			return false, fmt.Errorf("update subscription lifecycle: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit subscription lifecycle: %w", err)
	}
	return true, nil
}
