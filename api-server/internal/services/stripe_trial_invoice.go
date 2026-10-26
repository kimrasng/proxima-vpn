package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// Invoice creates one receipted order per paid invoice. A durable invoice/order
// link survives crashes between order creation and the existing settlement flow.
func (s *StripeTrialService) Invoice(ctx context.Context, event payments.TrialEvent) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin subscription invoice: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var localID, userID, planID, currency string
	var price int64
	var days int
	err = tx.QueryRow(ctx, `SELECT id,user_id,plan_id,currency,price_cents,duration_days
		FROM stripe_trial_subscriptions WHERE subscription_id=$1 FOR UPDATE`, event.SubscriptionID).
		Scan(&localID, &userID, &planID, &currency, &price, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		if event.LocalID != "" {
			return false, fmt.Errorf("trial invoice arrived before checkout confirmation")
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock subscription invoice: %w", err)
	}
	if event.Kind == "invoice.paid" {
		var settled bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stripe_trial_invoices i
			JOIN plan_orders o ON o.id=i.order_id WHERE i.invoice_id=$1 AND i.subscription_id=$2
			AND o.status='paid' AND o.granted_plan_expires_after IS NOT NULL)`,
			event.InvoiceID, localID).Scan(&settled); err != nil {
			return false, fmt.Errorf("read completed subscription invoice: %w", err)
		}
		if settled {
			return true, nil
		}
	}
	if event.Kind == "invoice.payment_failed" {
		state, err := s.gateway.ReadTrial(ctx, event.SubscriptionID)
		if err != nil {
			return false, fmt.Errorf("reconcile failed invoice subscription: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE stripe_trial_subscriptions
			SET status=$2,cancel_at_period_end=$3,updated_at=NOW() WHERE id=$1 AND status<>'canceled'`,
			localID, state.Status, state.CancelAtEnd); err != nil {
			return false, fmt.Errorf("record trial payment failure: %w", err)
		}
		return true, tx.Commit(ctx)
	}
	// Stripe creates a zero-value invoice at trial start; this is not a paid
	// renewal and must not buy an extra paid period.
	if event.AmountCents == 0 {
		return true, nil
	}
	if event.Kind != "invoice.paid" || event.Status != "paid" ||
		event.AmountCents != price || event.Currency != currency {
		return false, fmt.Errorf("subscription invoice differs from recurring price")
	}
	state, err := s.gateway.ReadTrial(ctx, event.SubscriptionID)
	if err != nil {
		return false, fmt.Errorf("verify renewal recurring terms: %w", err)
	}
	if state.ID != event.SubscriptionID || state.LocalID != localID ||
		event.BillingReason != "subscription_cycle" || event.Proration ||
		event.Quantity != 1 || state.Quantity != 1 ||
		event.ItemID == "" || event.ItemID != state.ItemID ||
		state.DurationDays != int64(days) || state.PriceCents != price || state.Currency != currency ||
		event.PeriodEnd-event.PeriodStart != int64(days)*86400 {
		return false, fmt.Errorf("invoice is not the contracted recurring service period")
	}
	var orderID string
	err = tx.QueryRow(ctx, `SELECT order_id FROM stripe_trial_invoices WHERE invoice_id=$1`, event.InvoiceID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		var overlaps bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stripe_trial_invoices
			WHERE subscription_id=$1 AND period_start<$3 AND period_end>$2)`,
			localID, event.PeriodStart, event.PeriodEnd).Scan(&overlaps); err != nil {
			return false, fmt.Errorf("check billed period overlap: %w", err)
		}
		if overlaps {
			return false, fmt.Errorf("invoice service period was already granted")
		}
		if err := tx.QueryRow(ctx, `INSERT INTO plan_orders
			(user_id,plan_id,duration_days,price_cents,provider,provider_session_id,expires_at,status,paid_at)
			VALUES($1,$2,$3,$4,'stripe_subscription',$5,NOW()+INTERVAL '1 day','paid',NOW())
			RETURNING id::text`, userID, planID, days, price, event.InvoiceID).Scan(&orderID); err != nil {
			return false, fmt.Errorf("create renewal order: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO stripe_trial_invoices(invoice_id,subscription_id,order_id,period_start,period_end)
			VALUES($1,$2,$3,$4,$5)`, event.InvoiceID, localID, orderID, event.PeriodStart, event.PeriodEnd); err != nil {
			return false, fmt.Errorf("link renewal invoice: %w", err)
		}
	} else if err != nil {
		return false, fmt.Errorf("find renewal order: %w", err)
	}
	if err := settleTrialOrderTx(ctx, tx, orderID, nil); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit renewal settlement: %w", err)
	}
	return true, nil
}
