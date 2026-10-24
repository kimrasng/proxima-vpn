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

// ResetTicketHandler serves the user and admin reset-ticket endpoints.
type ResetTicketHandler struct {
	svc *services.ResetTicketService
}

// NewResetTicketHandler creates a ResetTicketHandler.
func NewResetTicketHandler(db *pgxpool.Pool) *ResetTicketHandler {
	return &ResetTicketHandler{svc: services.NewResetTicketService(db)}
}

// ListMine handles GET /api/v1/user/reset-tickets.
// @Summary List my reset tickets
// @Description Returns the data reset tickets the user can still use, soonest-expiring first
// @Tags user-portal
// @Produce json
// @Success 200 {array} services.ResetTicket
// @Security BearerAuth
// @Router /user/reset-tickets [get]
func (h *ResetTicketHandler) ListMine(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	tickets, err := h.svc.ListUsable(context.Background(), userID)
	if err != nil {
		log.Printf("[reset-tickets] list for %s failed: %v", userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(tickets)
}

// Redeem handles POST /api/v1/user/reset-tickets/:id/redeem.
// @Summary Use a reset ticket
// @Description Spends one of the user's tickets and resets this period's data usage to zero
// @Tags user-portal
// @Produce json
// @Param id path string true "Ticket ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /user/reset-tickets/{id}/redeem [post]
func (h *ResetTicketHandler) Redeem(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	ticketID := c.Params("id")
	if !isUUID(ticketID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "reset ticket not found"})
	}
	email, _ := c.Locals("email").(string)
	res, err := h.svc.Redeem(context.Background(), userID, ticketID, email)
	switch {
	case errors.Is(err, services.ErrResetTicketNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "reset ticket not found"})
	case errors.Is(err, services.ErrResetTicketUsed):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "reset ticket already used", "code": "used"})
	case errors.Is(err, services.ErrResetTicketExpired):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "reset ticket expired", "code": "expired"})
	case errors.Is(err, services.ErrResetNotApplicable):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "no capped plan to reset", "code": "no_plan"})
	case err != nil:
		log.Printf("[reset-tickets] redeem %s for %s failed: %v", ticketID, userID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(fiber.Map{"name": res.TicketName, "reset_bytes": res.ResetBytes})
}

type grantResetTicketRequest struct {
	Name       string `json:"name"`
	TargetType string `json:"target_type"`
	UserID     string `json:"user_id"`
	PlanID     string `json:"plan_id"`
	ExpiresAt  string `json:"expires_at"`
}

// Grant handles POST /api/v1/admin/reset-tickets.
// @Summary Grant reset tickets
// @Description Issues one reset ticket to a single user, every user on a plan, or every account
// @Tags admin-reset-tickets
// @Accept json
// @Produce json
// @Param body body grantResetTicketRequest true "Grant"
// @Success 201 {object} services.ResetTicketGrant
// @Failure 400 {object} map[string]string
// @Failure 422 {object} map[string]string
// @Security BearerAuth
// @Router /admin/reset-tickets [post]
func (h *ResetTicketHandler) Grant(c *fiber.Ctx) error {
	var req grantResetTicketRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	expiresAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "expires_at must be an RFC 3339 timestamp"})
	}
	req.Name = strings.TrimSpace(req.Name)
	if len([]rune(req.Name)) > 80 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is too long"})
	}
	if (req.TargetType == services.ResetTargetUser && !isUUID(req.UserID)) ||
		(req.TargetType == services.ResetTargetPlan && !isUUID(req.PlanID)) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "a valid target id is required"})
	}
	g, err := h.svc.Grant(context.Background(), services.GrantRequest{
		Name: req.Name, TargetType: req.TargetType, UserID: req.UserID, PlanID: req.PlanID,
		ExpiresAt: expiresAt, ActorID: adminIDOf(c), ActorLabel: adminEmail(c),
	})
	switch {
	case errors.Is(err, services.ErrResetInvalidGrant):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, target and a future expiry are required"})
	case errors.Is(err, services.ErrResetNoRecipients):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": "no account matches this target", "code": "no_recipients"})
	case err != nil:
		log.Printf("[reset-tickets] grant failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.Status(fiber.StatusCreated).JSON(g)
}

// ListGrants handles GET /api/v1/admin/reset-tickets.
// @Summary List reset ticket grants
// @Description Returns the most recent grants with how many tickets each issued and how many were used
// @Tags admin-reset-tickets
// @Produce json
// @Success 200 {array} services.ResetTicketGrant
// @Security BearerAuth
// @Router /admin/reset-tickets [get]
func (h *ResetTicketHandler) ListGrants(c *fiber.Ctx) error {
	grants, err := h.svc.ListGrants(context.Background(), 200)
	if err != nil {
		log.Printf("[reset-tickets] list grants failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(grants)
}
