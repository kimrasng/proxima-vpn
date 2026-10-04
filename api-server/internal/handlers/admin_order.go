package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// AdminOrderHandler handles admin review of user-placed plan orders.
// MarkPaid is the administrator-confirmed payment provider's confirmation
// route - see internal/payments for the provider abstraction and
// services.PaymentService for the state machine every provider settles into.
type AdminOrderHandler struct {
	db        *pgxpool.Pool
	payments  *services.PaymentService
	promotion *services.PromotionService
	activity  *services.ActivityService
}

// NewAdminOrderHandler creates a new AdminOrderHandler.
func NewAdminOrderHandler(db *pgxpool.Pool) *AdminOrderHandler {
	return &AdminOrderHandler{
		db:        db,
		payments:  services.NewPaymentService(db),
		promotion: services.NewPromotionService(db),
		activity:  services.NewActivityService(db),
	}
}

// adminOrderItem is one row of the admin order list. Existing keys and types
// are frozen - the admin UI already reads them - so the context summary below
// is additive and omitempty, absent for a pre-capture or purged order. Full
// context, grant evidence and payment events live on Audit.
type adminOrderItem struct {
	ID            string     `json:"id"`
	UserEmail     string     `json:"user_email"`
	UserName      string     `json:"user_name"`
	PlanID        string     `json:"plan_id"`
	PlanName      string     `json:"plan_name"`
	DurationDays  int        `json:"duration_days"`
	PriceCents    int64      `json:"price_cents"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	PaidBy        string     `json:"paid_by,omitempty"`
	CancelledAt   *time.Time `json:"cancelled_at,omitempty"`
	ClientIP      string     `json:"client_ip,omitempty"`
	BrowserFamily string     `json:"browser_family,omitempty"`
	OSFamily      string     `json:"os_family,omitempty"`
}

// List handles GET /api/v1/admin/orders.
// @Summary List plan orders
// @Description Returns all plan orders, optionally filtered by status
// @Tags admin-orders
// @Produce json
// @Param status query string false "Filter by status (pending, paid, cancelled)"
// @Success 200 {array} adminOrderItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/orders [get]
func (h *AdminOrderHandler) List(c *fiber.Ctx) error {
	statusFilter := c.Query("status")

	query := `SELECT o.id, u.email, u.name, o.plan_id, p.name, o.duration_days, o.price_cents,
	                 o.status, o.created_at, o.paid_at, o.paid_by, o.cancelled_at,
	                 o.client_ip, o.browser_family, o.os_family
	          FROM plan_orders o
	          JOIN users u ON u.id = o.user_id
	          JOIN plans p ON p.id = o.plan_id`
	args := []interface{}{}
	if statusFilter != "" {
		query += ` WHERE o.status = $1`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY o.created_at DESC`

	rows, err := h.db.Query(context.Background(), query, args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	defer rows.Close()

	results := make([]adminOrderItem, 0)
	for rows.Next() {
		var o adminOrderItem
		var clientIP, browserFamily, osFamily *string
		if err := rows.Scan(&o.ID, &o.UserEmail, &o.UserName, &o.PlanID, &o.PlanName, &o.DurationDays, &o.PriceCents,
			&o.Status, &o.CreatedAt, &o.PaidAt, &o.PaidBy, &o.CancelledAt,
			&clientIP, &browserFamily, &osFamily); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
		}
		o.ClientIP = derefOrEmpty(clientIP)
		o.BrowserFamily = derefOrEmpty(browserFamily)
		o.OSFamily = derefOrEmpty(osFamily)
		results = append(results, o)
	}

	return c.JSON(results)
}

// MarkPaid handles POST /api/v1/admin/orders/:id/pay.
//
// This route is the administrator-confirmed provider's confirmation channel:
// it builds a Confirmation directly (there is no signature to verify - the
// admin JWT already authenticated the caller) and hands it to
// PaymentService.Settle, which owns the order state machine for every
// provider. AmountCents is left at zero: an admin is asserting the order's
// own snapshotted price was received, not reporting a third party's figure
// for Settle to cross-check.
// @Summary Mark a plan order paid
// @Description Admin confirms payment was received for a pending order, granting the plan
// @Tags admin-orders
// @Produce json
// @Param id path string true "Order ID"
// @Success 200 {object} adminOrderItem
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/orders/{id}/pay [post]
func (h *AdminOrderHandler) MarkPaid(c *fiber.Ctx) error {
	id := c.Params("id")
	adminID := c.Locals("admin_id").(string)
	ctx := context.Background()

	requestID, _ := c.Locals("requestid").(string)
	res, err := h.payments.Settle(ctx, payments.Confirmation{
		Provider:   payments.ProviderAdmin,
		ExternalID: "order:" + id,
		OrderID:    id,
		RequestIP:  c.IP(),
		UserAgent:  c.Get("User-Agent"),
		RequestID:  requestID,
		ActorType:  "admin",
		ActorID:    adminID,
		ActorLabel: adminEmail(c),
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to grant plan",
		})
	}
	switch res.Outcome {
	case services.SettleDuplicate:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "order already confirmed"})
	case services.SettleIgnored:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "order not found or already resolved"})
	}

	var result adminOrderItem
	err = h.db.QueryRow(ctx,
		`SELECT o.id, u.email, u.name, o.plan_id, p.name, o.duration_days, o.price_cents,
		        o.status, o.created_at, o.paid_at, o.paid_by, o.cancelled_at
		 FROM plan_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN plans p ON p.id = o.plan_id
		 WHERE o.id = $1`,
		id,
	).Scan(&result.ID, &result.UserEmail, &result.UserName, &result.PlanID, &result.PlanName, &result.DurationDays,
		&result.PriceCents, &result.Status, &result.CreatedAt, &result.PaidAt, &result.PaidBy, &result.CancelledAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	return c.JSON(result)
}

// Cancel handles POST /api/v1/admin/orders/:id/cancel. Same effect as the
// user's own Cancel, available to an admin for a stale or mistaken order.
// @Summary Cancel a plan order
// @Description Admin cancels a pending order
// @Tags admin-orders
// @Produce json
// @Param id path string true "Order ID"
// @Success 200 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /admin/orders/{id}/cancel [post]
func (h *AdminOrderHandler) Cancel(c *fiber.Ctx) error {
	id := c.Params("id")
	adminID := c.Locals("admin_id").(string)

	tag, err := h.db.Exec(context.Background(),
		`UPDATE plan_orders SET status = 'cancelled', cancelled_at = NOW()
		 WHERE id = $1 AND status = 'pending'`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "order not found or already resolved",
		})
	}
	if err := h.promotion.Release(context.Background(), id); err != nil {
		fmt.Printf("promotions: failed to release order %s on admin cancel: %v\n", id, err)
	}

	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventOrderCancelled,
		Severity:   services.SeverityInfo,
		ActorType:  "admin",
		ActorID:    adminID,
		ActorLabel: adminEmail(c),
		TargetType: "plan_order",
		TargetID:   id,
	})

	return c.JSON(fiber.Map{"message": "order cancelled"})
}
