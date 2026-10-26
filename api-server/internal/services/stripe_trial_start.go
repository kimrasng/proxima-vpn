package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

var ErrTrialSubscriptionExists = errors.New("an open trial subscription already exists")

type StripeTrialService struct {
	db      *pgxpool.Pool
	gateway payments.TrialGateway
}

func NewStripeTrialService(db *pgxpool.Pool, gateway payments.TrialGateway) *StripeTrialService {
	return &StripeTrialService{db: db, gateway: gateway}
}

// Start claims a cap slot before contacting Stripe. Expired Checkout attempts
// release their slots; a completed session is granted by the webhook, not here.
func (s *StripeTrialService) Start(ctx context.Context, userID, code string, visitors ...PromoVisitor) (*payments.CheckoutHandoff, error) {
	return s.StartForOrder(ctx, userID, code, "", visitors...)
}

func (s *StripeTrialService) StartForOrder(ctx context.Context, userID, code, orderID string, visitors ...PromoVisitor) (*payments.CheckoutHandoff, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin trial checkout: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var promotionID, planID string
	var duration, trialDays, perUser int
	var maximum *int
	var active, requiresCard bool
	err = tx.QueryRow(ctx, `SELECT id,plan_id,duration_days,trial_days,
		max_redemptions,max_redemptions_per_user,is_active,requires_card
		FROM free_trial_promotions WHERE code=$1 FOR UPDATE`, code).
		Scan(&promotionID, &planID, &duration, &trialDays, &maximum, &perUser, &active, &requiresCard)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &PromotionRejectError{Reason: RejectNotFound}
	}
	if err != nil {
		return nil, fmt.Errorf("load trial checkout promotion: %w", err)
	}
	if !active {
		return nil, &PromotionRejectError{Reason: RejectInactive}
	}
	if !requiresCard {
		return nil, &PromotionRejectError{Reason: RejectPlanNotEligible}
	}
	var email string
	var hasPlan bool
	if err := tx.QueryRow(ctx, `SELECT email,COALESCE(plan_expires_at>NOW(),false)
		FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&email, &hasPlan); err != nil {
		return nil, fmt.Errorf("lock trial user: %w", err)
	}
	if hasPlan {
		return nil, &PromotionRejectError{Reason: RejectActivePlan}
	}
	var visitor PromoVisitor
	if len(visitors) > 0 {
		visitor = visitors[0]
	}
	visitor.Email = email
	if err := CheckPromoEligibility(ctx, tx, code, visitor); err != nil {
		return nil, err
	}
	// Only Stripe-confirmed expiration releases a reservation. A local clock
	// deadline cannot prove that the customer did not complete remotely.
	var hasOpen, hasOrder bool
	if err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM stripe_trial_subscriptions WHERE user_id=$1 AND status NOT IN ('expired','canceled')),
		EXISTS(SELECT 1 FROM plan_orders WHERE user_id=$1 AND status='pending' AND id::text<>$2)`, userID, orderID).
		Scan(&hasOpen, &hasOrder); err != nil {
		return nil, fmt.Errorf("check trial conflicts: %w", err)
	}
	if hasOpen {
		var retry payments.TrialCheckoutRequest
		var savedSession *string
		var savedOrder *string
		err := tx.QueryRow(ctx, `SELECT id::text,price_cents,currency,duration_days,trial_days,checkout_expires_at
			,checkout_session_id,trial_order_id::text
			FROM stripe_trial_subscriptions WHERE user_id=$1 AND promotion_id=$2
			AND status='checkout'`, userID, promotionID).
			Scan(&retry.ID, &retry.PriceCents, &retry.Currency, &retry.DurationDays, &retry.TrialDays, &retry.ExpiresAt, &savedSession, &savedOrder)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTrialSubscriptionExists
		}
		if err != nil {
			return nil, fmt.Errorf("read uncertain trial checkout: %w", err)
		}
		if orderID != "" && (savedOrder == nil || *savedOrder != orderID) {
			return nil, ErrTrialSubscriptionExists
		}
		retry.UserEmail = email
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit trial checkout resume: %w", err)
		}
		if savedSession != nil {
			event, err := s.gateway.ReadTrialCheckout(ctx, *savedSession)
			if err != nil {
				return nil, fmt.Errorf("resume saved trial checkout: %w", err)
			}
			if event == nil || event.LocalID != retry.ID {
				return nil, fmt.Errorf("saved trial checkout identity mismatch")
			}
			switch event.Kind {
			case "checkout.session.open":
				return &payments.CheckoutHandoff{Mode: payments.ModeRedirect, RedirectURL: event.CheckoutURL, SessionID: *savedSession}, nil
			case "checkout.session.expired":
				if _, err := s.Process(ctx, *event); err != nil {
					return nil, err
				}
				if orderID != "" {
					if _, err := s.db.Exec(ctx, `UPDATE stripe_trial_subscriptions SET trial_order_id=NULL WHERE id=$1 AND status='expired'`, retry.ID); err != nil {
						return nil, fmt.Errorf("release expired trial order: %w", err)
					}
				}
				return s.StartForOrder(ctx, userID, code, orderID, visitor)
			case "checkout.session.completed":
				if err := s.Complete(ctx, *event); err != nil {
					return nil, err
				}
				return &payments.CheckoutHandoff{Mode: payments.ModeRedirect, RedirectURL: "/portal/plan"}, nil
			default:
				return nil, fmt.Errorf("unsupported saved trial checkout state")
			}
		}
		return s.startOrReplace(ctx, retry, code, visitor)
	}
	if hasOrder {
		return nil, ErrTrialPendingOrder
	}
	var orderPrice int64
	if orderID != "" {
		var matches bool
		if err := tx.QueryRow(ctx, `SELECT plan_id=$3 AND free_trial_id=$4 AND duration_days=$5
			AND status='pending',price_cents+discount_cents FROM plan_orders WHERE id::text=$1 AND user_id=$2 FOR UPDATE`,
			orderID, userID, planID, promotionID, duration).Scan(&matches, &orderPrice); err != nil {
			return nil, fmt.Errorf("check trial order: %w", err)
		}
		if !matches {
			return nil, &PromotionRejectError{Reason: RejectPlanNotEligible}
		}
	}
	var total, mine int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*),COUNT(*) FILTER(WHERE user_id=$2)
		FROM (
			SELECT user_id FROM free_trial_redemptions WHERE promotion_id=$1
			UNION ALL
			SELECT user_id FROM stripe_trial_subscriptions
			WHERE promotion_id=$1 AND status<>'expired'
		) claims`, promotionID, userID).Scan(&total, &mine); err != nil {
		return nil, fmt.Errorf("count trial claims: %w", err)
	}
	if maximum != nil && total >= *maximum {
		return nil, &PromotionRejectError{Reason: RejectOverallCapReached}
	}
	if mine >= perUser {
		return nil, &PromotionRejectError{Reason: RejectUserCapReached}
	}
	var price int64
	var planActive bool
	var lockedPlan string
	if err := tx.QueryRow(ctx, `SELECT id FROM plans WHERE id=$1 FOR SHARE`, planID).Scan(&lockedPlan); err != nil {
		return nil, fmt.Errorf("lock trial plan: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT p.is_active,pp.price_cents FROM plans p
		JOIN plan_prices pp ON pp.plan_id=p.id AND pp.duration_days=$2
		WHERE p.id=$1`, planID, duration).Scan(&planActive, &price)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!planActive || price <= 0 || duration > 365 || trialDays > 730) {
		return nil, &PromotionRejectError{Reason: RejectPlanNotEligible}
	}
	if err != nil {
		return nil, fmt.Errorf("read trial recurring price: %w", err)
	}
	if orderID != "" {
		price = orderPrice
		if _, err := tx.Exec(ctx, `UPDATE stripe_trial_subscriptions SET trial_order_id=NULL
			WHERE trial_order_id::text=$1 AND user_id=$2 AND status='expired'`, orderID, userID); err != nil {
			return nil, fmt.Errorf("release expired trial order link: %w", err)
		}
	}
	var id string
	var expires time.Time
	if err := tx.QueryRow(ctx, `INSERT INTO stripe_trial_subscriptions
		(user_id,promotion_id,plan_id,price_cents,currency,duration_days,trial_days,checkout_expires_at,trial_order_id)
		VALUES($1,$2,$3,$4,$7,$5,$6,NOW()+INTERVAL '1 hour',NULLIF($8,'')::uuid)
		RETURNING id::text,checkout_expires_at`,
		userID, promotionID, planID, price, duration, trialDays, s.gateway.TrialCurrency(), orderID).Scan(&id, &expires); err != nil {
		return nil, fmt.Errorf("claim trial subscription: %w", err)
	}
	if orderID != "" {
		if _, err := tx.Exec(ctx, `UPDATE plan_orders SET provider='free_trial',expires_at=$2 WHERE id::text=$1`, orderID, expires); err != nil {
			return nil, fmt.Errorf("bind pending trial order: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit trial checkout claim: %w", err)
	}
	return s.startOrReplace(ctx, payments.TrialCheckoutRequest{
		ID: id, UserEmail: email, Currency: s.gateway.TrialCurrency(), PriceCents: price,
		DurationDays: duration, TrialDays: trialDays, ExpiresAt: expires,
	}, code, visitor)
}

func (s *StripeTrialService) startCheckout(ctx context.Context, req payments.TrialCheckoutRequest) (*payments.CheckoutHandoff, error) {
	handoff, err := s.gateway.StartTrial(ctx, req)
	if err != nil {
		// Stripe may have created the session before a transport timeout.
		// Preserve the claim and its idempotency key until a retry or expiry.
		return nil, fmt.Errorf("start Stripe trial: %w", err)
	}
	if _, err := s.db.Exec(context.WithoutCancel(ctx), `UPDATE stripe_trial_subscriptions
		SET checkout_session_id=$2,updated_at=NOW() WHERE id=$1`, req.ID, handoff.SessionID); err != nil {
		return nil, fmt.Errorf("save trial checkout session: %w", err)
	}
	return handoff, nil
}
