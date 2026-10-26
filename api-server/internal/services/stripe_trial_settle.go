package services

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// settleTrialOrderTx commits the entitlement, order and audit in the caller's
// transaction. There is no paid-but-not-granted crash window.
func settleTrialOrderTx(ctx context.Context, tx pgx.Tx, orderID string, trialEnd *time.Time) error {
	var userID, planID, provider, status string
	var days int
	var price int64
	var granted *time.Time
	if err := tx.QueryRow(ctx, `SELECT user_id,plan_id,provider,status,duration_days,
		price_cents,granted_plan_expires_after FROM plan_orders WHERE id=$1 FOR UPDATE`, orderID).
		Scan(&userID, &planID, &provider, &status, &days, &price, &granted); err != nil {
		return fmt.Errorf("lock trial settlement order: %w", err)
	}
	if status == "paid" && granted != nil {
		return nil
	}
	// Renewal orders are inserted paid inside this same transaction, avoiding
	// the customer's unrelated one-pending-order checkout constraint.
	if status != "pending" && (status != "paid" || provider != "stripe_subscription" || granted != nil) {
		return fmt.Errorf("trial order is not pending")
	}
	var result *GrantResult
	if trialEnd != nil {
		var currentEnd *time.Time
		var hasAccess bool
		if err := tx.QueryRow(ctx, `SELECT plan_expires_at,COALESCE(plan_expires_at>NOW(),false)
			FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&currentEnd, &hasAccess); err != nil {
			return fmt.Errorf("lock absolute trial entitlement: %w", err)
		}
		if hasAccess {
			// A competing paid/admin grant wins; do not replace its plan or
			// shorten/extend its expiry with a free trial.
			result = &GrantResult{PlanExpiresBefore: currentEnd, PlanExpiresAt: *currentEnd}
			trialEnd = nil
		}
	}
	if result == nil {
		var err error
		result, err = grantPlanTx(ctx, tx, userID, planID, &days)
		if err != nil {
			return fmt.Errorf("grant trial billing entitlement: %w", err)
		}
	}
	if trialEnd != nil {
		if _, err := tx.Exec(ctx, `UPDATE users SET plan_expires_at=$2 WHERE id=$1`, userID, *trialEnd); err != nil {
			return fmt.Errorf("anchor trial entitlement: %w", err)
		}
		result.PlanExpiresAt = *trialEnd
	}
	if _, err := tx.Exec(ctx, `UPDATE plan_orders SET status='paid',paid_at=NOW(),paid_by='stripe',
		granted_plan_expires_before=$2,granted_plan_expires_after=$3 WHERE id=$1`,
		orderID, result.PlanExpiresBefore, result.PlanExpiresAt); err != nil {
		return fmt.Errorf("record trial billing receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_events
		(provider,external_id,order_id,amount_cents,currency,session_id,payload,
		request_ip,user_agent,request_id,actor_type,actor_id,outcome,reason)
		VALUES($1,$2,$3,$4,'','','{}','','','','provider','stripe','granted','')
		ON CONFLICT(provider,external_id) DO NOTHING`,
		provider, "trial-order:"+orderID, orderID, price); err != nil {
		return fmt.Errorf("audit trial billing settlement: %w", err)
	}
	return nil
}
