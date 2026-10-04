package handlers

import (
	"context"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// PaymentWebhookHandler is the single dispatch point every hosted provider's
// callback reaches. It looks the provider up in the registry, authenticates
// the payload through that provider's own WebhookVerifier, and hands the
// resulting Confirmation to PaymentService.Settle - adding a provider never
// means editing this file, only registering it (see payments.Build).
type PaymentWebhookHandler struct {
	registry payments.Registry
	payments *services.PaymentService
}

// NewPaymentWebhookHandler creates a new PaymentWebhookHandler.
func NewPaymentWebhookHandler(db *pgxpool.Pool, registry payments.Registry) *PaymentWebhookHandler {
	return &PaymentWebhookHandler{registry: registry, payments: services.NewPaymentService(db)}
}

// Receive handles POST /webhooks/payments/:provider. Status discipline
// matters here more than in any other route in this codebase: Stripe (and
// any future hosted provider) retries a webhook forever until it gets a 2xx,
// so every outcome that is not "please try again" - duplicate, ignored,
// unknown provider, an authentic-but-irrelevant event - must still answer
// 200. Only a failed signature check (400) and a genuine grant failure that
// should be retried (500) get anything else.
func (h *PaymentWebhookHandler) Receive(c *fiber.Ctx) error {
	provider, ok := h.registry.Get(c.Params("provider"))
	if !ok {
		return c.SendStatus(fiber.StatusOK)
	}
	verifier, ok := provider.(payments.WebhookVerifier)
	if !ok {
		return c.SendStatus(fiber.StatusOK)
	}

	// Copy before use: c.Body() aliases a fasthttp-owned buffer that is not
	// valid once the handler returns, and this payload is persisted.
	body := append([]byte(nil), c.Body()...)

	conf, err := verifier.VerifyWebhook(body, func(key string) string { return c.Get(key) })
	if err != nil {
		log.Printf("[payments] %s webhook signature failed: %v", provider.Name(), err)
		return c.SendStatus(fiber.StatusBadRequest)
	}
	if conf == nil {
		// Authentic, but not an event this provider's caller acts on.
		return c.SendStatus(fiber.StatusOK)
	}

	conf.RequestIP = c.IP()
	conf.UserAgent = c.Get("User-Agent")
	requestID, _ := c.Locals("requestid").(string)
	conf.RequestID = requestID

	res, err := h.payments.Settle(context.Background(), *conf)
	if err != nil {
		log.Printf("[payments] %s webhook grant failed for order %s: %v", provider.Name(), conf.OrderID, err)
		return c.SendStatus(fiber.StatusInternalServerError)
	}

	log.Printf("[payments] %s webhook settled order %s: %s", provider.Name(), conf.OrderID, res.Outcome)
	return c.SendStatus(fiber.StatusOK)
}
