package services

import (
	"context"
	"fmt"

	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

func (s *StripeTrialService) Process(ctx context.Context, event payments.TrialEvent) (bool, error) {
	switch event.Kind {
	case "checkout.session.completed":
		return true, s.Complete(ctx, event)
	case "checkout.session.expired":
		_, err := s.db.Exec(ctx, `UPDATE stripe_trial_subscriptions SET
			status='expired',updated_at=NOW() WHERE id=$1 AND status='checkout'
			AND (checkout_session_id=$2 OR checkout_session_id IS NULL)`,
			event.LocalID, event.SessionID)
		if err != nil {
			return false, fmt.Errorf("release expired trial checkout: %w", err)
		}
		return true, nil
	case "invoice.paid", "invoice.payment_failed":
		return s.Invoice(ctx, event)
	case "customer.subscription.updated", "customer.subscription.deleted":
		return s.ApplyLifecycle(ctx, event)
	default:
		return false, nil
	}
}
