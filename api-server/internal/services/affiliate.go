package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AffiliateReservation is what ReserveAffiliateTx hands back: the partner the
// order is now attributed to and the customer discount to snapshot on it.
type AffiliateReservation struct {
	PartnerID     string
	DiscountCents int64
}

// IsAffiliateCodeTx reports whether code names an affiliate partner, so a
// caller can route it away from the coupon ledger before reserving anything.
func IsAffiliateCodeTx(ctx context.Context, tx pgx.Tx, code string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM affiliate_partners WHERE code = $1)`, code).Scan(&exists); err != nil {
		return false, fmt.Errorf("look up affiliate code: %w", err)
	}
	return exists, nil
}

// IsFreeTrialCodeTx reports whether code names a free-trial promotion. Trial
// codes grant a plan through FreeTrialService.Redeem, never a discount.
func IsFreeTrialCodeTx(ctx context.Context, tx pgx.Tx, code string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM free_trial_promotions WHERE code = $1)`, code).Scan(&exists); err != nil {
		return false, fmt.Errorf("look up free trial code: %w", err)
	}
	return exists, nil
}

// ReserveAffiliateTx attributes a pending order to the partner owning code and
// computes the customer discount. Partners have no redemption caps, so there
// is no ledger row to claim; the caller, which already holds the order row
// lock, snapshots the result onto plan_orders in the same transaction.
func ReserveAffiliateTx(ctx context.Context, tx pgx.Tx, code string, orderCents int64) (*AffiliateReservation, error) {
	var partnerID, discountType string
	var discountValue int64
	var active bool
	err := tx.QueryRow(ctx,
		`SELECT id, discount_type, discount_value, is_active
		 FROM affiliate_partners WHERE code = $1 FOR SHARE`, code,
	).Scan(&partnerID, &discountType, &discountValue, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &PromotionRejectError{Reason: RejectNotFound}
	}
	if err != nil {
		return nil, fmt.Errorf("load affiliate partner: %w", err)
	}
	if !active {
		return nil, &PromotionRejectError{Reason: RejectInactive}
	}
	return &AffiliateReservation{
		PartnerID:     partnerID,
		DiscountCents: computeDiscountCents(discountType, discountValue, orderCents),
	}, nil
}

// recordAffiliateCommissionTx books the partner's share inside the settle claim,
// so the commission commits exactly when the order becomes paid. The unique
// index on order_id makes a replayed settlement a no-op instead of a second
// payout; the commission uses the amount actually paid.
func recordAffiliateCommissionTx(ctx context.Context, tx pgx.Tx, orderID string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO affiliate_commissions (partner_id, order_id, order_amount, commission_amount)
		 SELECT o.affiliate_partner_id, o.id, o.price_cents,
		        ROUND(o.price_cents * a.commission_rate / 100)::bigint
		 FROM plan_orders o
		 JOIN affiliate_partners a ON a.id = o.affiliate_partner_id
		 WHERE o.id = $1 AND o.status = 'paid'
		 ON CONFLICT (order_id) WHERE order_id IS NOT NULL DO NOTHING`,
		orderID,
	)
	if err != nil {
		return fmt.Errorf("record affiliate commission: %w", err)
	}
	return nil
}
