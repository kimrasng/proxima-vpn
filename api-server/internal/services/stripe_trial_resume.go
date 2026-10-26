package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

func (s *StripeTrialService) startOrReplace(ctx context.Context, req payments.TrialCheckoutRequest, code string) (*payments.CheckoutHandoff, error) {
	handoff, err := s.startCheckout(ctx, req)
	if errors.Is(err, payments.ErrTrialCheckoutAbsent) {
		return s.replaceAbsentCheckout(ctx, req.ID, code)
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
		return s.replaceAbsentCheckout(ctx, req.ID, code)
	case "checkout.session.open":
		return handoff, nil
	default:
		return nil, fmt.Errorf("unsupported recovered checkout state")
	}
}

func (s *StripeTrialService) replaceAbsentCheckout(ctx context.Context, id, code string) (*payments.CheckoutHandoff, error) {
	var userID string
	if err := s.db.QueryRow(ctx, `UPDATE stripe_trial_subscriptions
		SET status='expired',updated_at=NOW() WHERE id=$1 AND status IN ('checkout','expired')
		RETURNING user_id::text`, id).Scan(&userID); err != nil {
		return nil, fmt.Errorf("release remotely absent checkout: %w", err)
	}
	return s.Start(ctx, userID, code)
}
