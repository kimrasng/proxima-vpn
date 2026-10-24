package handlers

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// AdminPlanGrantHandler lets an administrator issue an existing plan to a user
// as a receipted order, and lists that user's orders.
type AdminPlanGrantHandler struct {
	db     *pgxpool.Pool
	grants *services.PlanGrantService
}

// NewAdminPlanGrantHandler creates an AdminPlanGrantHandler.
func NewAdminPlanGrantHandler(db *pgxpool.Pool) *AdminPlanGrantHandler {
	return &AdminPlanGrantHandler{db: db, grants: services.NewPlanGrantService(db)}
}

type planGrantRequest struct {
	PlanID       string `json:"plan_id"`
	DurationDays int    `json:"duration_days"`
	PriceCents   int64  `json:"price_cents"`
	Note         string `json:"note"`
}

type adminUserOrder struct {
	ID            string     `json:"id"`
	PlanName      string     `json:"plan_name"`
	DurationDays  int        `json:"duration_days"`
	PriceCents    int64      `json:"price_cents"`
	Status        string     `json:"status"`
	Provider      string     `json:"provider"`
	Note          string     `json:"note"`
	CreatedAt     time.Time  `json:"created_at"`
	PaidAt        *time.Time `json:"paid_at"`
	PaidBy        string     `json:"paid_by"`
	ExpiresBefore *time.Time `json:"granted_plan_expires_before"`
	ExpiresAfter  *time.Time `json:"granted_plan_expires_after"`
}

// Grant handles POST /api/v1/admin/users/:id/plan-grants.
// @Summary Grant a plan to a user
// @Description Issues an existing plan for a number of days as a paid order, so it appears in the user's orders and receipt
// @Tags admin-users
// @Accept json
// @Produce json
// @Param id path string true "User ID"
// @Param body body planGrantRequest true "Grant"
// @Success 201 {object} adminUserOrder
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id}/plan-grants [post]
func (h *AdminPlanGrantHandler) Grant(c *fiber.Ctx) error {
	userID := c.Params("id")
	var req planGrantRequest
	if err := c.BodyParser(&req); err != nil || !isUUID(userID) || !isUUID(req.PlanID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "a user, plan and duration are required"})
	}
	requestID, _ := c.Locals("requestid").(string)
	orderID, _, err := h.grants.Grant(context.Background(), services.PlanGrantRequest{
		UserID: userID, PlanID: req.PlanID, DurationDays: req.DurationDays, PriceCents: req.PriceCents,
		Note: strings.TrimSpace(req.Note), ActorID: adminIDOf(c), ActorLabel: adminEmail(c),
		RequestIP: c.IP(), UserAgent: c.Get(fiber.HeaderUserAgent), RequestID: requestID,
	})
	switch {
	case errors.Is(err, services.ErrGrantInvalid):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "duration must be 1-3650 days and price cannot be negative"})
	case errors.Is(err, services.ErrGrantPlanNotFound), errors.Is(err, services.ErrGrantUserNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, services.ErrGrantPendingOrder):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "the user has an order awaiting payment", "code": "pending_order"})
	case err != nil:
		log.Printf("[plan-grant] grant to %s failed: %v", userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to grant plan"})
	}
	orders, err := h.listOrders(c.Context(), userID, orderID)
	if err != nil || len(orders) == 0 {
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": orderID})
	}
	return c.Status(fiber.StatusCreated).JSON(orders[0])
}

// ListOrders handles GET /api/v1/admin/users/:id/orders.
// @Summary List a user's orders
// @Description Every order of the user, newest first, including administrator grants
// @Tags admin-users
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {array} adminUserOrder
// @Security BearerAuth
// @Router /admin/users/{id}/orders [get]
func (h *AdminPlanGrantHandler) ListOrders(c *fiber.Ctx) error {
	userID := c.Params("id")
	if !isUUID(userID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}
	orders, err := h.listOrders(c.Context(), userID, "")
	if err != nil {
		log.Printf("[plan-grant] list orders for %s failed: %v", userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(orders)
}

func (h *AdminPlanGrantHandler) listOrders(ctx context.Context, userID, onlyOrderID string) ([]adminUserOrder, error) {
	rows, err := h.db.Query(ctx,
		`SELECT o.id::text, p.name, o.duration_days, o.price_cents, o.status, o.provider,
		        CASE WHEN o.provider = $3 THEN o.provider_session_id ELSE '' END,
		        o.created_at, o.paid_at, o.paid_by, o.granted_plan_expires_before, o.granted_plan_expires_after
		 FROM plan_orders o JOIN plans p ON p.id = o.plan_id
		 WHERE o.user_id = $1 AND ($2 = '' OR o.id::text = $2)
		 ORDER BY o.created_at DESC LIMIT 100`,
		userID, onlyOrderID, services.ProviderGrant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]adminUserOrder, 0)
	for rows.Next() {
		var o adminUserOrder
		if err := rows.Scan(&o.ID, &o.PlanName, &o.DurationDays, &o.PriceCents, &o.Status, &o.Provider, &o.Note,
			&o.CreatedAt, &o.PaidAt, &o.PaidBy, &o.ExpiresBefore, &o.ExpiresAfter); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
