package handlers

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// UserFreeTrialHandler lets a signed-in user redeem a free-trial code.
type UserFreeTrialHandler struct {
	trials *services.FreeTrialService
}

// NewUserFreeTrialHandler creates a UserFreeTrialHandler.
func NewUserFreeTrialHandler(db *pgxpool.Pool) *UserFreeTrialHandler {
	return &UserFreeTrialHandler{trials: services.NewFreeTrialService(db)}
}

type redeemFreeTrialRequest struct {
	Code string `json:"code"`
}

type redeemFreeTrialResponse struct {
	OrderID       string    `json:"order_id"`
	PlanName      string    `json:"plan_name"`
	TrialDays     int       `json:"trial_days"`
	PlanExpiresAt time.Time `json:"plan_expires_at"`
}

// Redeem handles POST /api/v1/user/free-trials/redeem.
// @Summary Redeem a free-trial code
// @Tags user-orders
// @Accept json
// @Produce json
// @Param body body redeemFreeTrialRequest true "Trial code"
// @Success 201 {object} redeemFreeTrialResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /user/free-trials/redeem [post]
func (h *UserFreeTrialHandler) Redeem(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var req redeemFreeTrialRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "code is required"})
	}
	res, err := h.trials.Redeem(c.UserContext(), userID, code, promoVisitor(c))
	if err != nil {
		var reject *services.PromotionRejectError
		if errors.As(err, &reject) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "promotion_rejected", "reason": string(reject.Reason)})
		}
		if errors.Is(err, services.ErrTrialPendingOrder) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "you already have a pending order"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.Status(fiber.StatusCreated).JSON(redeemFreeTrialResponse{
		OrderID: res.OrderID, PlanName: res.PlanName, TrialDays: res.TrialDays, PlanExpiresAt: res.PlanExpiresAt,
	})
}
