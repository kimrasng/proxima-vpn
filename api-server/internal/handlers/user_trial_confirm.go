package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func (h *UserTrialSubscriptionHandler) Confirm(c *fiber.Ctx) error {
	var req struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
	}
	if err := c.BodyParser(&req); err != nil || req.ID == "" || req.SessionID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "id and session_id are required"})
	}
	// Resolve ownership before contacting Stripe. A browser-supplied session
	// cannot apply a trial to a different account.
	var owned bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT EXISTS(
		SELECT 1 FROM stripe_trial_subscriptions
		WHERE id::text=$1 AND user_id=$2 AND checkout_session_id=$3)`,
		req.ID, c.Locals("user_id").(string), req.SessionID).Scan(&owned); err != nil {
		return trialSubscriptionError(c, err)
	}
	if !owned {
		return c.SendStatus(404)
	}
	provider, ok := h.registry.Get(payments.ProviderStripe)
	if !ok {
		return c.SendStatus(503)
	}
	gateway, ok := provider.(payments.TrialGateway)
	if !ok {
		return c.SendStatus(503)
	}
	event, err := gateway.ReadTrialCheckout(c.UserContext(), req.SessionID)
	if err != nil {
		return trialSubscriptionError(c, err)
	}
	if event == nil || event.LocalID != req.ID {
		return c.Status(400).JSON(fiber.Map{"error": "checkout does not match this trial"})
	}
	if err := services.NewStripeTrialService(h.db, gateway).Complete(c.UserContext(), *event); err != nil {
		return trialSubscriptionError(c, err)
	}
	return h.Current(c)
}
