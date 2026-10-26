package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// ProviderFreeTrial marks a zero-amount order created by redeeming a
// free-trial code, so history and receipts can label it as a trial.
const ProviderFreeTrial = "free_trial"

// ErrTrialPendingOrder: the user already has an order awaiting payment.
var ErrTrialPendingOrder = errors.New("user has a pending order")

// FreeTrialRedemption is the order a trial code produced and the term it set.
type FreeTrialRedemption struct {
	OrderID       string
	PlanName      string
	TrialDays     int
	PlanExpiresAt time.Time
}

// FreeTrialService grants plans from free-trial codes as recorded orders.
type FreeTrialService struct {
	db       *pgxpool.Pool
	payments *PaymentService
}

// NewFreeTrialService creates a FreeTrialService.
func NewFreeTrialService(db *pgxpool.Pool) *FreeTrialService {
	return &FreeTrialService{db: db, payments: NewPaymentService(db)}
}

// Redeem checks the code and claims a redemption slot in one transaction that
// holds the promotion row lock, so the overall and per-user caps stay exact
// under concurrent redemption. The order then settles through PaymentService
// like an admin grant, which extends or starts the plan by the purchase rules.
func (s *FreeTrialService) Redeem(ctx context.Context, userID, code string, visitors ...PromoVisitor) (*FreeTrialRedemption, error) {
	return s.RedeemOrder(ctx, userID, code, "", visitors...)
}

func (s *FreeTrialService) RedeemOrder(ctx context.Context, userID, code, existingOrder string, visitors ...PromoVisitor) (*FreeTrialRedemption, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin trial redeem: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var promotionID string
	var planID *string
	var trialDays, perUser int
	var maxRedemptions *int
	var requiresCard, active bool
	err = tx.QueryRow(ctx,
		`SELECT id, plan_id, trial_days, max_redemptions, max_redemptions_per_user, requires_card, is_active
		 FROM free_trial_promotions WHERE code = $1 FOR UPDATE`, code,
	).Scan(&promotionID, &planID, &trialDays, &maxRedemptions, &perUser, &requiresCard, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &PromotionRejectError{Reason: RejectNotFound}
	}
	if err != nil {
		return nil, fmt.Errorf("load free trial: %w", err)
	}
	if !active {
		return nil, &PromotionRejectError{Reason: RejectInactive}
	}
	if requiresCard {
		return nil, &PromotionRejectError{Reason: RejectCardRequiredUnsupported}
	}
	if planID == nil {
		return nil, &PromotionRejectError{Reason: RejectPlanNotEligible}
	}
	var planName string
	var planActive bool
	err = tx.QueryRow(ctx, `SELECT name, is_active FROM plans WHERE id = $1 FOR SHARE`, *planID).Scan(&planName, &planActive)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !planActive) {
		return nil, &PromotionRejectError{Reason: RejectPlanNotEligible}
	}
	if err != nil {
		return nil, fmt.Errorf("load trial plan: %w", err)
	}
	var hasActivePlan bool
	var visitor PromoVisitor
	if len(visitors) > 0 {
		visitor = visitors[0]
	}
	if err := tx.QueryRow(ctx,
		`SELECT plan_expires_at IS NOT NULL AND plan_expires_at > NOW(),email FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&hasActivePlan, &visitor.Email); err != nil {
		return nil, fmt.Errorf("load user plan: %w", err)
	}
	if hasActivePlan {
		return nil, &PromotionRejectError{Reason: RejectActivePlan}
	}
	if err := CheckPromoEligibility(ctx, tx, code, visitor); err != nil {
		return nil, err
	}

	var total, mine int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE user_id = $2)
		 FROM free_trial_redemptions WHERE promotion_id = $1`, promotionID, userID,
	).Scan(&total, &mine); err != nil {
		return nil, fmt.Errorf("count trial redemptions: %w", err)
	}
	if maxRedemptions != nil && total >= *maxRedemptions {
		return nil, &PromotionRejectError{Reason: RejectOverallCapReached}
	}
	if mine >= perUser {
		return nil, &PromotionRejectError{Reason: RejectUserCapReached}
	}

	orderID := existingOrder
	if existingOrder == "" {
		err = tx.QueryRow(ctx,
			`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status, provider,
		                          provider_session_id, expires_at, free_trial_id)
		 VALUES ($1, $2, $3, 0, 'pending', $4, $5, NOW() + INTERVAL '10 minutes', $6)
		 RETURNING id::text`,
			userID, *planID, trialDays, ProviderFreeTrial, code, promotionID,
		).Scan(&orderID)
	} else {
		err = tx.QueryRow(ctx, `UPDATE plan_orders SET duration_days=$4,provider=$5,provider_session_id=$6
			WHERE id::text=$1 AND user_id=$2 AND plan_id=$3 AND free_trial_id=$7 AND status='pending'
			RETURNING id::text`, existingOrder, userID, *planID, trialDays, ProviderFreeTrial, code, promotionID).Scan(&orderID)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrTrialPendingOrder
	}
	if err != nil {
		return nil, fmt.Errorf("create trial order: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO free_trial_redemptions (promotion_id, user_id, order_id) VALUES ($1, $2, $3)`,
		promotionID, userID, orderID,
	); err != nil {
		return nil, fmt.Errorf("record trial redemption: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit trial redeem: %w", err)
	}

	res, err := s.payments.Settle(ctx, payments.Confirmation{
		Provider:   ProviderFreeTrial,
		ExternalID: "trial:" + orderID,
		OrderID:    orderID,
		ActorType:  "user",
		ActorID:    userID,
		ActorLabel: "free trial " + code,
	})
	if err != nil || res.Outcome != SettleGranted {
		// Give the slot back: a trial that never granted must not count.
		cleanup := context.WithoutCancel(ctx)
		_, _ = s.db.Exec(cleanup, `DELETE FROM free_trial_redemptions WHERE order_id = $1`, orderID)
		_, _ = s.db.Exec(cleanup,
			`UPDATE plan_orders SET status = 'cancelled', cancelled_at = NOW() WHERE id = $1 AND status = 'pending'`, orderID)
		if err != nil {
			return nil, fmt.Errorf("settle trial: %w", err)
		}
		return nil, fmt.Errorf("settle trial: %s", res.Reason)
	}
	return &FreeTrialRedemption{
		OrderID: orderID, PlanName: planName, TrialDays: trialDays, PlanExpiresAt: res.Grant.PlanExpiresAt,
	}, nil
}
