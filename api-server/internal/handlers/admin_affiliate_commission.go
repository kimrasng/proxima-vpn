package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
)

type affiliateCommission struct {
	ID               string    `json:"id"`
	PartnerID        *string   `json:"partner_id"`
	OrderID          *string   `json:"order_id"`
	OrderAmount      int64     `json:"order_amount"`
	CommissionAmount int64     `json:"commission_amount"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
}

func (h *AdminAffiliateHandler) Commissions(c *fiber.Ctx) error {
	if !promotionKindID(c, "id") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}
	// Check existence even for partners with no commissions.
	var exists bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT EXISTS(SELECT 1 FROM affiliate_partners WHERE id=$1)`,
		c.Params("id")).Scan(&exists); err != nil {
		return promotionKindError(c, err)
	}
	if !exists {
		return c.Status(404).JSON(fiber.Map{"error": "partner not found"})
	}
	rows, err := h.db.Query(c.UserContext(), `SELECT id,partner_id,order_id,order_amount,commission_amount,status,created_at
		FROM affiliate_commissions WHERE partner_id=$1 ORDER BY created_at DESC, id`, c.Params("id"))
	if err != nil {
		return promotionKindError(c, err)
	}
	defer rows.Close()
	items := make([]affiliateCommission, 0)
	var revenue, owed, paid int64
	orders := make(map[string]bool)
	for rows.Next() {
		var item affiliateCommission
		if err := rows.Scan(&item.ID, &item.PartnerID, &item.OrderID, &item.OrderAmount, &item.CommissionAmount,
			&item.Status, &item.CreatedAt); err != nil {
			return promotionKindError(c, err)
		}
		items = append(items, item)
		if item.OrderID != nil && !orders[*item.OrderID] {
			orders[*item.OrderID] = true
			revenue += item.OrderAmount
		}
		if item.Status == "paid" {
			paid += item.CommissionAmount
		} else {
			owed += item.CommissionAmount
		}
	}
	if err := rows.Err(); err != nil {
		return promotionKindError(c, err)
	}
	return c.JSON(fiber.Map{"commissions": items, "total_orders": len(orders), "total_revenue": revenue,
		"commission_owed": owed, "commission_paid": paid})
}

func (h *AdminAffiliateHandler) MarkPaid(c *fiber.Ctx) error {
	if !promotionKindID(c, "id") || !promotionKindID(c, "commissionId") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}
	tag, err := h.db.Exec(c.UserContext(), `UPDATE affiliate_commissions SET status='paid'
		WHERE id=$1 AND partner_id=$2`, c.Params("commissionId"), c.Params("id"))
	if err != nil {
		return promotionKindError(c, err)
	}
	if tag.RowsAffected() == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "commission not found"})
	}
	return c.JSON(fiber.Map{"status": "paid"})
}
