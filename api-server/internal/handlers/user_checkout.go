package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
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
	payments *services.PaymentService
}

// NewUserCheckoutHandler creates a new UserCheckoutHandler.
func NewUserCheckoutHandler(db *pgxpool.Pool, registry payments.Registry) *UserCheckoutHandler {
	return &UserCheckoutHandler{db: db, registry: registry, payments: services.NewPaymentService(db)}
}

type confirmCheckoutRequest struct {
	SessionID string `json:"session_id"`
}

type confirmCheckoutResponse struct {
	Status string `json:"status"`
}

// Confirm handles POST /api/v1/user/orders/:id/checkout/confirm. The browser
// calls it when it returns from hosted checkout, so a payment is applied even
// when the provider's webhook cannot reach this server. Nothing the browser
// sends is trusted beyond the session id: the provider is asked for the
// session, its order_id metadata must name this caller's own order, and the
// grant goes through the same idempotent Settle the webhook uses.
// @Summary Confirm a returning hosted checkout
// @Description Reads the checkout session back from the provider and settles the order if it is paid
// @Tags user-orders
// @Accept json
// @Produce json
// @Param id path string true "Order ID"
// @Param body body confirmCheckoutRequest true "Checkout session to confirm"
// @Success 200 {object} confirmCheckoutResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Security BearerAuth
// @Router /user/orders/{id}/checkout/confirm [post]
func (h *UserCheckoutHandler) Confirm(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	orderID := c.Params("id")

	var req confirmCheckoutRequest
	if err := c.BodyParser(&req); err != nil || req.SessionID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "session_id is required"})
	}

	ctx := context.Background()
	var providerName, status string
	err := h.db.QueryRow(ctx,
		`SELECT COALESCE(provider, ''), status FROM plan_orders WHERE id = $1 AND user_id = $2`,
		orderID, userID,
	).Scan(&providerName, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "order not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if status == "paid" {
		return c.JSON(confirmCheckoutResponse{Status: status})
	}

	provider, ok := h.registry.Get(providerName)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "order has no hosted checkout"})
	}
	confirmer, ok := provider.(payments.SessionConfirmer)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "provider cannot confirm sessions"})
	}

	conf, err := confirmer.ConfirmSession(ctx, req.SessionID)
	if err != nil {
		log.Printf("[payments] %s session confirm failed for order %s: %v", provider.Name(), orderID, err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "could not verify the checkout session"})
	}
	if conf == nil {
		return c.JSON(confirmCheckoutResponse{Status: status})
	}
	if conf.OrderID != orderID {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "session does not belong to this order"})
	}

	conf.RequestIP = c.IP()
	conf.UserAgent = c.Get("User-Agent")
	requestID, _ := c.Locals("requestid").(string)
	conf.RequestID = requestID

	if _, err := h.payments.Settle(ctx, *conf); err != nil {
		log.Printf("[payments] %s session settle failed for order %s: %v", provider.Name(), orderID, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if err := h.db.QueryRow(ctx, `SELECT status FROM plan_orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(confirmCheckoutResponse{Status: status})
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
	var planID, planName, userEmail, promoCode, promoName string
	var durationDays int
	var priceCents, discountCents int64
	var expiresAt sql.NullTime
	err := h.db.QueryRow(ctx,
		`SELECT o.plan_id, p.name, u.email, o.duration_days, o.price_cents,
		        o.discount_cents, COALESCE(pc.code, ap.code, ft.code, ''), COALESCE(page.headline, ''), o.expires_at
		 FROM plan_orders o
		 JOIN plans p ON p.id = o.plan_id
		 JOIN users u ON u.id = o.user_id
		 LEFT JOIN promotion_codes pc ON pc.id = o.promotion_id
		 LEFT JOIN affiliate_partners ap ON ap.id = o.affiliate_partner_id
		 LEFT JOIN free_trial_promotions ft ON ft.id = o.free_trial_id
		 LEFT JOIN promo_landing_pages page ON page.code = COALESCE(pc.code, ap.code, ft.code)
		 WHERE o.id = $1 AND o.user_id = $2 AND o.status = 'pending'`,
		orderID, userID,
	).Scan(&planID, &planName, &userEmail, &durationDays, &priceCents, &discountCents, &promoCode, &promoName, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "order not found, not yours, or already resolved",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	if promoCode != "" {
		visitor := promoVisitor(c)
		visitor.Email = userEmail
		if err := services.CheckPromoEligibility(ctx, h.db, promoCode, visitor); err != nil {
			return trialSubscriptionError(c, err)
		}
		var isTrial, requiresCard bool
		if err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plan_orders WHERE id=$1 AND free_trial_id IS NOT NULL),
			COALESCE((SELECT t.requires_card FROM plan_orders o JOIN free_trial_promotions t ON t.id=o.free_trial_id WHERE o.id=$1),false)`,
			orderID).Scan(&isTrial, &requiresCard); err != nil {
			return c.SendStatus(500)
		}
		if isTrial {
			if !requiresCard {
				if _, err := services.NewFreeTrialService(h.db).RedeemOrder(ctx, userID, promoCode, orderID, visitor); err != nil {
					return trialSubscriptionError(c, err)
				}
				return c.JSON(startCheckoutResponse{Provider: services.ProviderFreeTrial, Mode: string(payments.ModeManual)})
			}
			gateway, ok := provider.(payments.TrialGateway)
			if !ok {
				return c.Status(400).JSON(fiber.Map{"error": "trial requires Stripe card registration"})
			}
			handoff, err := services.NewStripeTrialService(h.db, gateway).StartForOrder(ctx, userID, promoCode, orderID, visitor)
			if err != nil {
				return trialSubscriptionError(c, err)
			}
			return c.JSON(startCheckoutResponse{Provider: provider.Name(), Mode: string(handoff.Mode), RedirectURL: handoff.RedirectURL})
		}
	}
	req2 := payments.CheckoutRequest{
		OrderID:       orderID,
		UserID:        userID,
		UserEmail:     userEmail,
		PlanName:      planName,
		DurationDays:  durationDays,
		PriceCents:    priceCents,
		DiscountCents: discountCents,
		PromotionCode: promoCode,
		PromotionName: promoName,
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

type orderReceiptResponse struct {
	OrderID       string     `json:"order_id"`
	PlanName      string     `json:"plan_name"`
	DurationDays  int        `json:"duration_days"`
	PriceCents    int64      `json:"price_cents"`
	Discount      int64      `json:"discount_cents"`
	PromotionCode string     `json:"promotion_code,omitempty"`
	PromotionName string     `json:"promotion_name,omitempty"`
	Currency      string     `json:"currency"`
	Provider      string     `json:"provider"`
	Email         string     `json:"email"`
	PaidAt        *time.Time `json:"paid_at"`
	CreatedAt     time.Time  `json:"created_at"`
	ReceiptURL    string     `json:"receipt_url,omitempty"`
	// Note is the administrator's reason for a granted order; empty otherwise.
	Note          string     `json:"note,omitempty"`
	ExpiresBefore *time.Time `json:"plan_expires_before,omitempty"`
	ExpiresAfter  *time.Time `json:"plan_expires_after,omitempty"`
}

// Receipt handles GET /api/v1/user/orders/:id/receipt. Only the order's own
// user can read it, and only once it is paid. When the provider hosts a
// receipt (Stripe), its URL is included; the panel-side summary is always
// returned so a zero-total or admin-settled order still has a receipt.
// @Summary Get the receipt of my paid order
// @Tags user-orders
// @Produce json
// @Param id path string true "Order ID"
// @Success 200 {object} orderReceiptResponse
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /user/orders/{id}/receipt [get]
func (h *UserCheckoutHandler) Receipt(c *fiber.Ctx) error {
	return h.writeReceipt(c, c.Params("id"), c.Locals("user_id").(string))
}

// AdminReceipt handles GET /api/v1/admin/users/:id/orders/:orderId/receipt.
// It returns the same receipt the user sees, so support can read exactly what
// the customer was shown.
// @Summary Read a user's order receipt
// @Description Receipt of one of the user's paid orders, as the user sees it
// @Tags admin-users
// @Produce json
// @Param id path string true "User ID"
// @Param orderId path string true "Order ID"
// @Success 200 {object} orderReceiptResponse
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id}/orders/{orderId}/receipt [get]
func (h *UserCheckoutHandler) AdminReceipt(c *fiber.Ctx) error {
	userID, orderID := c.Params("id"), c.Params("orderId")
	if !isUUID(userID) || !isUUID(orderID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paid order not found"})
	}
	return h.writeReceipt(c, orderID, userID)
}

func (h *UserCheckoutHandler) writeReceipt(c *fiber.Ctx, orderID, userID string) error {
	ctx := c.Context()
	var r orderReceiptResponse
	var sessionID string
	err := h.db.QueryRow(ctx,
		`SELECT o.id, p.name, o.duration_days, o.price_cents, o.discount_cents,
		        o.provider, o.provider_session_id, u.email, o.paid_at,
		        o.granted_plan_expires_before, o.granted_plan_expires_after, o.created_at,
		        COALESCE(pc.code, ap.code, ft.code, ''), COALESCE(page.headline, '')
		 FROM plan_orders o
		 JOIN plans p ON p.id = o.plan_id
		 JOIN users u ON u.id = o.user_id
		 LEFT JOIN promotion_codes pc ON pc.id = o.promotion_id
		 LEFT JOIN affiliate_partners ap ON ap.id = o.affiliate_partner_id
		 LEFT JOIN free_trial_promotions ft ON ft.id = o.free_trial_id
		 LEFT JOIN promo_landing_pages page ON page.code = COALESCE(pc.code, ap.code, ft.code)
		 WHERE o.id = $1 AND o.user_id = $2 AND o.status = 'paid'`,
		orderID, userID,
	).Scan(&r.OrderID, &r.PlanName, &r.DurationDays, &r.PriceCents, &r.Discount,
		&r.Provider, &sessionID, &r.Email, &r.PaidAt, &r.ExpiresBefore, &r.ExpiresAfter, &r.CreatedAt,
		&r.PromotionCode, &r.PromotionName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "paid order not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	_ = h.db.QueryRow(ctx,
		`SELECT currency FROM payment_events
		 WHERE order_id = $1 AND outcome = 'granted' AND currency <> ''
		 ORDER BY received_at DESC LIMIT 1`, orderID,
	).Scan(&r.Currency)

	if r.Provider == services.ProviderGrant {
		r.Note, sessionID = sessionID, ""
	}
	if sessionID != "" {
		if provider, ok := h.registry.Get(receiptProviderName(r.Provider, sessionID)); ok {
			if rp, ok := provider.(payments.ReceiptProvider); ok {
				url, err := rp.ReceiptURL(ctx, sessionID)
				if err != nil {
					log.Printf("[payments] receipt lookup for order %s failed: %v", orderID, err)
				} else {
					r.ReceiptURL = url
				}
			}
		}
	}
	return c.JSON(r)
}

// receiptProviderName names the provider that issued an order's receipt. A card
// free trial is stored as provider "free_trial" but its session is a Stripe
// Checkout Session, so its invoice has to be read from Stripe; a trial redeemed
// without a card keeps its promotion code there and has no remote receipt.
func receiptProviderName(orderProvider, sessionID string) string {
	if orderProvider == services.ProviderFreeTrial && strings.HasPrefix(sessionID, "cs_") {
		return payments.ProviderStripe
	}
	return orderProvider
}
