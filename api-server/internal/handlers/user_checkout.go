package handlers

import (
	"context"
	"database/sql"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// UserCheckoutHandler lets a user start a checkout for one of their own
// pending orders and lists which providers are available to pay with. The
// order itself (POST /user/orders) is unchanged and untouched by this file -
// an order exists before a provider is ever contacted, which is what makes
// "an order is persisted for every checkout attempt whether or not it is
// paid" hold even for an abandoned hosted-checkout session.
type UserCheckoutHandler struct {
	db       *pgxpool.Pool
	registry payments.Registry
}

// NewUserCheckoutHandler creates a new UserCheckoutHandler.
func NewUserCheckoutHandler(db *pgxpool.Pool, registry payments.Registry) *UserCheckoutHandler {
	return &UserCheckoutHandler{db: db, registry: registry}
}

type startCheckoutRequest struct {
	Provider string `json:"provider"`
}

type startCheckoutResponse struct {
	Provider    string `json:"provider"`
	Mode        string `json:"mode"`
	RedirectURL string `json:"redirect_url,omitempty"`
}

type paymentProviderItem struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// Start handles POST /api/v1/user/orders/:id/checkout. The route is
// provider-agnostic - the provider name is a request field, not a path
// segment - so adding a provider never means adding a route.
// @Summary Start a checkout for a pending order
// @Description Hands an existing pending order to the named payment provider and returns where to send the browser
// @Tags user-orders
// @Accept json
// @Produce json
// @Param id path string true "Order ID"
// @Param body body startCheckoutRequest true "Which provider to check out with"
// @Success 200 {object} startCheckoutResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/orders/{id}/checkout [post]
func (h *UserCheckoutHandler) Start(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	orderID := c.Params("id")

	var req startCheckoutRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Provider == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "provider is required"})
	}

	provider, ok := h.registry.Get(req.Provider)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unknown provider"})
	}

	ctx := context.Background()
	var planID, planName, userEmail string
	var durationDays int
	var priceCents int64
	var expiresAt sql.NullTime
	err := h.db.QueryRow(ctx,
		`SELECT o.plan_id, p.name, u.email, o.duration_days, o.price_cents, o.expires_at
		 FROM plan_orders o
		 JOIN plans p ON p.id = o.plan_id
		 JOIN users u ON u.id = o.user_id
		 WHERE o.id = $1 AND o.user_id = $2 AND o.status = 'pending'`,
		orderID, userID,
	).Scan(&planID, &planName, &userEmail, &durationDays, &priceCents, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "order not found, not yours, or already resolved",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	req2 := payments.CheckoutRequest{
		OrderID:      orderID,
		UserID:       userID,
		UserEmail:    userEmail,
		PlanName:     planName,
		DurationDays: durationDays,
		PriceCents:   priceCents,
	}
	if expiresAt.Valid {
		req2.ExpiresAt = expiresAt.Time
	}

	handoff, err := provider.StartCheckout(ctx, req2)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to start checkout"})
	}

	// COALESCE, not assignment: create time saw the request that actually
	// placed the order, so checkout only fills context that was never captured
	// (a pre-capture row) and never replaces a more truthful create-time value.
	reqCtx := captureCheckoutContext(c, "")
	if _, err := h.db.Exec(ctx,
		`UPDATE plan_orders SET
		   provider = $2,
		   provider_session_id = $3,
		   origin = COALESCE(origin, $4),
		   client_ip = COALESCE(client_ip, $5),
		   user_agent = COALESCE(user_agent, $6),
		   browser_family = COALESCE(browser_family, $7),
		   os_family = COALESCE(os_family, $8),
		   locale = COALESCE(locale, $9)
		 WHERE id = $1`,
		orderID, provider.Name(), handoff.SessionID,
		reqCtx.OriginHost, reqCtx.ClientIP, reqCtx.UserAgent,
		reqCtx.BrowserFamily, reqCtx.OSFamily, reqCtx.Locale,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	return c.JSON(startCheckoutResponse{
		Provider:    provider.Name(),
		Mode:        string(handoff.Mode),
		RedirectURL: handoff.RedirectURL,
	})
}

// ListProviders handles GET /api/v1/user/payment-providers.
// @Summary List available payment providers
// @Description Returns every registered payment provider and how checking out with it behaves
// @Tags user-orders
// @Produce json
// @Success 200 {array} paymentProviderItem
// @Security BearerAuth
// @Router /user/payment-providers [get]
func (h *UserCheckoutHandler) ListProviders(c *fiber.Ctx) error {
	items := make([]paymentProviderItem, 0, len(h.registry))
	for _, name := range h.registry.Names() {
		provider, _ := h.registry.Get(name)
		mode := string(payments.ModeManual)
		if _, ok := provider.(payments.WebhookVerifier); ok {
			mode = string(payments.ModeRedirect)
		}
		items = append(items, paymentProviderItem{Name: name, Mode: mode})
	}
	return c.JSON(items)
}
