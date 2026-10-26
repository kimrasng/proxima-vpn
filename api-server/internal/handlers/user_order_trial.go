package handlers

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func (h *UserOrderHandler) applyTrialTx(ctx context.Context, tx pgx.Tx, resp, updated *orderResponse, original int64, userID, code string) error {
	var id, planID string
	var duration, days int
	var active, hasPlan bool
	if err := tx.QueryRow(ctx, `SELECT id,plan_id,duration_days,trial_days,is_active
		FROM free_trial_promotions WHERE code=$1 FOR UPDATE`, code).
		Scan(&id, &planID, &duration, &days, &active); err != nil {
		return fmt.Errorf("read trial order promotion: %w", err)
	}
	if !active {
		return &services.PromotionRejectError{Reason: services.RejectInactive}
	}
	if updated.PlanID != planID || updated.DurationDays != duration {
		return &services.PromotionRejectError{Reason: services.RejectPlanNotEligible}
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(plan_expires_at>NOW(),false) FROM users WHERE id=$1`, userID).Scan(&hasPlan); err != nil {
		return fmt.Errorf("read trial entitlement: %w", err)
	}
	if hasPlan {
		return &services.PromotionRejectError{Reason: services.RejectActivePlan}
	}
	if err := h.promotion.ReleaseTx(ctx, tx, updated.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE plan_orders SET free_trial_id=$2,promotion_id=NULL,
		affiliate_partner_id=NULL,price_cents=0,discount_cents=$3 WHERE id=$1`,
		updated.ID, id, original); err != nil {
		return fmt.Errorf("attach trial promotion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit trial promotion: %w", err)
	}
	updated.PriceCents, updated.DiscountCents, updated.PromotionCode = 0, original, code
	*resp = *updated
	return nil
}
