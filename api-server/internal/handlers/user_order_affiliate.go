package handlers

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// applyAffiliateTx swaps whatever discount the locked order carried for the
// partner's customer discount. A coupon reservation is released in the same
// transaction so its slot returns to the coupon's cap.
func (h *UserOrderHandler) applyAffiliateTx(ctx context.Context, tx pgx.Tx, resp, updated *orderResponse, originalCents int64, userID, code string) error {
	if err := h.promotion.ReleaseTx(ctx, tx, updated.ID); err != nil {
		return err
	}
	reservation, err := services.ReserveAffiliateTx(ctx, tx, code, originalCents)
	if err != nil {
		return err
	}

	updated.DiscountCents = reservation.DiscountCents
	updated.PriceCents = originalCents - reservation.DiscountCents
	if _, err := tx.Exec(ctx,
		`UPDATE plan_orders SET promotion_id = NULL, free_trial_id=NULL, affiliate_partner_id = $2, discount_cents = $3, price_cents = $4
		 WHERE id = $1 AND status = 'pending'`,
		updated.ID, reservation.PartnerID, updated.DiscountCents, updated.PriceCents,
	); err != nil {
		return fmt.Errorf("snapshot affiliate onto order: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit affiliate change: %w", err)
	}
	*resp = *updated
	resp.PromotionCode = code

	h.activity.Log(ctx, services.Record{
		EventType:  services.EventPromotionApplied,
		Severity:   services.SeverityInfo,
		ActorType:  "user",
		ActorID:    userID,
		TargetType: "plan_order",
		TargetID:   resp.ID,
		Detail: map[string]any{
			"promotion_code": code,
			"affiliate":      true,
			"discount_cents": reservation.DiscountCents,
		},
	})
	return nil
}
