package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

func (s *StripeTrialService) startOrReplace(ctx context.Context, req payments.TrialCheckoutRequest, code string, visitor PromoVisitor) (*payments.CheckoutHandoff, error) {
	handoff, err := s.startCheckout(ctx, req)
	if errors.Is(err, payments.ErrTrialCheckoutAbsent) {
		return s.replaceAbsentCheckout(ctx, req.ID, code, visitor)
	}
	if err != nil || req.ExpiresAt.After(time.Now()) {
		return handoff, err
	}
	event, err := s.gateway.ReadTrialCheckout(ctx, handoff.SessionID)
	if err != nil {
		return nil, fmt.Errorf("confirm recovered trial checkout: %w", err)
	}
	if event == nil || event.LocalID != req.ID {
		return nil, fmt.Errorf("recovered checkout identity mismatch")
	}
	switch event.Kind {
	case "checkout.session.completed":
		if err := s.Complete(ctx, *event); err != nil {
			return nil, err
		}
		return &payments.CheckoutHandoff{Mode: payments.ModeRedirect, RedirectURL: "/portal/plan"}, nil
	case "checkout.session.expired":
		if _, err := s.Process(ctx, *event); err != nil {
			return nil, err
		}
		return s.replaceAbsentCheckout(ctx, req.ID, code, visitor)
	case "checkout.session.open":
		return handoff, nil
	default:
		return nil, fmt.Errorf("unsupported recovered checkout state")
	}
}

func (s *StripeTrialService) replaceAbsentCheckout(ctx context.Context, id, code string, visitor PromoVisitor) (*payments.CheckoutHandoff, error) {
	var userID string
	var orderID *string
	if err := s.db.QueryRow(ctx, `UPDATE stripe_trial_subscriptions
		SET status='expired',updated_at=NOW() WHERE id=$1 AND status IN ('checkout','expired')
		RETURNING user_id::text,trial_order_id::text`, id).Scan(&userID, &orderID); err != nil {
		return nil, fmt.Errorf("release remotely absent checkout: %w", err)
	}
	if orderID != nil {
		if _, err := s.db.Exec(ctx, `UPDATE stripe_trial_subscriptions SET trial_order_id=NULL WHERE id=$1 AND status='expired'`, id); err != nil {
			return nil, fmt.Errorf("release old trial order link: %w", err)
		}
		return s.StartForOrder(ctx, userID, code, *orderID, visitor)
	}
	return s.Start(ctx, userID, code, visitor)
}
