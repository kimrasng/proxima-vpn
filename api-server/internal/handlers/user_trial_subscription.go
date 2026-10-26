package handlers

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type UserTrialSubscriptionHandler struct {
	db       *pgxpool.Pool
	registry payments.Registry
}

func NewUserTrialSubscriptionHandler(db *pgxpool.Pool, registry payments.Registry) *UserTrialSubscriptionHandler {
	return &UserTrialSubscriptionHandler{db: db, registry: registry}
}

func (h *UserTrialSubscriptionHandler) service() (*services.StripeTrialService, bool) {
	provider, exists := h.registry.Get(payments.ProviderStripe)
	if !exists {
		return nil, false
	}
	gateway, ok := provider.(payments.TrialGateway)
	if !ok {
		return nil, false
	}
	return services.NewStripeTrialService(h.db, gateway), true
}

func (h *UserTrialSubscriptionHandler) Start(c *fiber.Ctx) error {
	var req redeemFreeTrialRequest
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "code is required"})
	}
	code := strings.TrimSpace(req.Code)
	var requiresCard bool
	err := h.db.QueryRow(c.UserContext(), `SELECT requires_card FROM free_trial_promotions WHERE code=$1`, code).Scan(&requiresCard)
	if errors.Is(err, pgx.ErrNoRows) {
		return trialSubscriptionError(c, &services.PromotionRejectError{Reason: services.RejectNotFound})
	}
	if err != nil {
		return trialSubscriptionError(c, err)
	}
	if !requiresCard {
		return NewUserFreeTrialHandler(h.db).Redeem(c)
	}
	service, ok := h.service()
	if !ok {
		return c.Status(503).JSON(fiber.Map{"error": "Stripe subscription billing is unavailable"})
	}
	handoff, err := service.Start(c.UserContext(), c.Locals("user_id").(string), code, promoVisitor(c))
	if err != nil {
		return trialSubscriptionError(c, err)
	}
	return c.Status(201).JSON(fiber.Map{"mode": "redirect", "redirect_url": handoff.RedirectURL})
}

func (h *UserTrialSubscriptionHandler) Current(c *fiber.Ctx) error {
	service, ok := h.service()
	if !ok {
		// Stripe is not configured: still report the subscription row, just without a card.
		service = services.NewStripeTrialService(h.db, nil)
	}
	result, err := service.Current(c.UserContext(), c.Locals("user_id").(string))
	if err != nil {
		return trialSubscriptionError(c, err)
	}
	return c.JSON(result)
}

func (h *UserTrialSubscriptionHandler) Cancel(c *fiber.Ctx) error {
	service, ok := h.service()
	if !ok {
		return c.Status(503).JSON(fiber.Map{"error": "Stripe subscription billing is unavailable"})
	}
	if err := service.Cancel(c.UserContext(), c.Locals("user_id").(string)); err != nil {
		return trialSubscriptionError(c, err)
	}
	return h.Current(c)
}

func trialSubscriptionError(c *fiber.Ctx, err error) error {
	var reject *services.PromotionRejectError
	switch {
	case errors.As(err, &reject):
		return c.Status(400).JSON(fiber.Map{"error": "promotion_rejected", "reason": reject.Reason})
	case errors.Is(err, services.ErrTrialSubscriptionNotFound):
		return c.Status(404).JSON(fiber.Map{"error": "subscription not found"})
	case errors.Is(err, services.ErrTrialPendingOrder), errors.Is(err, services.ErrTrialSubscriptionExists):
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	default:
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
}
