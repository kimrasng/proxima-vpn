package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

func (h *UserTrialSubscriptionHandler) BillingPortal(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
	}
	provider, exists := h.registry.Get(payments.ProviderStripe)
	portal, ok := provider.(payments.BillingPortalProvider)
	if !exists || !ok {
		return c.Status(503).JSON(fiber.Map{"error": "Stripe billing portal is unavailable"})
	}

	// Keep canceled customers eligible; neither ownership nor the return URL
	// comes from the request body.
	var customerID string
	err := h.db.QueryRow(c.UserContext(), `
		SELECT customer_id FROM stripe_trial_subscriptions
		WHERE user_id=$1 AND customer_id IS NOT NULL
		ORDER BY created_at DESC LIMIT 1`, userID).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(404).JSON(fiber.Map{"error": "billing customer not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	url, err := portal.CreateBillingPortal(c.UserContext(), customerID)
	if err != nil {
		return c.Status(502).JSON(fiber.Map{"error": "could not open Stripe billing portal"})
	}
	return c.JSON(fiber.Map{"url": url})
}
