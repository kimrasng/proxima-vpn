package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// UserOrderHandler handles a user placing and managing their own plan orders.
type UserOrderHandler struct {
	db         *pgxpool.Pool
	activity   *services.ActivityService
	promotion  *services.PromotionService
	payment    *services.PaymentService
	pendingTTL time.Duration
}

// NewUserOrderHandler creates a new UserOrderHandler.
func NewUserOrderHandler(db *pgxpool.Pool, payments config.PaymentsConfig) *UserOrderHandler {
	return &UserOrderHandler{
		db:         db,
		activity:   services.NewActivityService(db),
		promotion:  services.NewPromotionService(db),
		payment:    services.NewPaymentService(db),
		pendingTTL: payments.PendingTTL(),
	}
}

type createOrderRequest struct {
	PlanID        string `json:"plan_id"`
	DurationDays  int    `json:"duration_days"`
	PromotionCode string `json:"promotion_code,omitempty"`
	// DeviceFingerprint is opaque audit data supplied by the panel in the JSON
	// body (the CORS allow-list admits no custom header for it). It is stored
	// and never consulted: no eligibility rule, rate limit, uniqueness check or
	// WHERE predicate reads this column.
	DeviceFingerprint string `json:"device_fingerprint,omitempty"`
}

type orderResponse struct {
	ID            string     `json:"id"`
	PlanID        string     `json:"plan_id"`
	PlanName      string     `json:"plan_name"`
	DurationDays  int        `json:"duration_days"`
	PriceCents    int64      `json:"price_cents"`
	DiscountCents int64      `json:"discount_cents,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	CancelledAt   *time.Time `json:"cancelled_at,omitempty"`
}

// Create handles POST /api/v1/user/orders. The price is never taken from the
// request body - it is re-read from plan_prices server-side, so a client
// cannot place an order at a price it made up.
// @Summary Create a plan order
// @Description Places a pending order for a plan at one of its priced durations
// @Tags user-orders
// @Accept json
// @Produce json
// @Param body body createOrderRequest true "Plan and duration to order"
// @Success 201 {object} orderResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/orders [post]
func (h *UserOrderHandler) Create(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req createOrderRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.PlanID == "" || req.DurationDays <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plan_id and duration_days are required",
		})
	}

	ctx := context.Background()

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Acquire the plan lock before reading prices. A joined locking SELECT
	// can retain a pre-wait snapshot of plan_prices while a concurrent plan
	// edit replaces them; the separate statement below gets a fresh snapshot.
	var lockedPlanID string
	if err := tx.QueryRow(ctx, `SELECT id FROM plans WHERE id = $1 FOR SHARE`, req.PlanID).Scan(&lockedPlanID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "plan is not priced at that duration"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	var planActive bool
	var planName string
	var priceCents int64
	err = tx.QueryRow(ctx,
		`SELECT p.is_active AND p.advertise AND p.is_advertised, p.name, pp.price_cents
		 FROM plans p
		 JOIN plan_prices pp ON pp.plan_id = p.id AND pp.duration_days = $2
		 WHERE p.id = $1`,
		req.PlanID, req.DurationDays,
	).Scan(&planActive, &planName, &priceCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "plan is not priced at that duration",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	if !planActive {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plan is not available for purchase",
		})
	}

	reqCtx := captureCheckoutContext(c, req.DeviceFingerprint)

	var resp orderResponse
	err = tx.QueryRow(ctx,
		`INSERT INTO plan_orders
		   (user_id, plan_id, duration_days, price_cents, expires_at,
		    origin, client_ip, user_agent, browser_family, os_family, locale, device_fingerprint)
		 VALUES ($1, $2, $3, $4, NOW() + $5::interval, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id, plan_id, duration_days, price_cents, status, created_at, expires_at`,
		userID, req.PlanID, req.DurationDays, priceCents, h.pendingTTL.String(),
		reqCtx.OriginHost, reqCtx.ClientIP, reqCtx.UserAgent,
		reqCtx.BrowserFamily, reqCtx.OSFamily, reqCtx.Locale, reqCtx.DeviceFingerprint,
	).Scan(&resp.ID, &resp.PlanID, &resp.DurationDays, &resp.PriceCents, &resp.Status, &resp.CreatedAt, &resp.ExpiresAt)
	if err != nil {
		// The partial unique index on one pending order per user is the
		// concurrency guard here, not an app-level check-then-insert that
		// would race against a second tab placing an order at the same time.
		if isPendingOrderConflict(err) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "you already have a pending order",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	resp.PlanName = planName

	h.activity.Log(ctx, services.Record{
		EventType:  services.EventOrderCreated,
		Severity:   services.SeverityInfo,
		ActorType:  "user",
		ActorID:    userID,
		TargetType: "plan_order",
		TargetID:   resp.ID,
		Detail: map[string]any{
			"plan":          planName,
			"duration_days": resp.DurationDays,
			"price_cents":   resp.PriceCents,
		},
	})

	var promotionWarning string
	if req.PromotionCode != "" {
		if applyErr := h.applyPromotion(ctx, &resp, userID, req.PromotionCode); applyErr != nil {
			var reject *services.PromotionRejectError
			if errors.As(applyErr, &reject) {
				promotionWarning = string(reject.Reason)
			} else {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "internal server error",
				})
			}
		}
	}

	if resp.PriceCents == 0 {
		if err := h.settleZeroCostOrder(ctx, resp.ID, userID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
		resp.Status = "paid"
	}

	if promotionWarning != "" {
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"order":              resp,
			"promotion_rejected": promotionWarning,
		})
	}

	return c.Status(fiber.StatusCreated).JSON(resp)
}

// applyPromotion reserves code against an order that already exists (so the
// per-order uniqueness on promotion_redemptions.order_id has something to
// reference), and on success snapshots the reservation onto plan_orders and
// mutates resp in place so the caller returns the discounted total. A
// rejection updates nothing and is returned as-is for Create/ApplyPromotion
// to translate into the caller-facing reason string.
func (h *UserOrderHandler) applyPromotion(ctx context.Context, resp *orderResponse, userID, code string) error {
	isFirstPurchase, err := h.isFirstPurchase(ctx, userID)
	if err != nil {
		return err
	}

	reservation, err := h.promotion.Reserve(ctx, code, userID, resp.ID, resp.PlanID, resp.DurationDays, resp.PriceCents, isFirstPurchase)
	if err != nil {
		return err
	}

	finalCents := resp.PriceCents - reservation.DiscountCents
	if _, err := h.db.Exec(ctx,
		`UPDATE plan_orders SET promotion_id = $2, discount_cents = $3, price_cents = $4 WHERE id = $1`,
		resp.ID, reservation.PromotionID, reservation.DiscountCents, finalCents,
	); err != nil {
		return fmt.Errorf("snapshot promotion onto order: %w", err)
	}

	resp.DiscountCents = reservation.DiscountCents
	resp.PriceCents = finalCents

	h.activity.Log(ctx, services.Record{
		EventType:  services.EventPromotionApplied,
		Severity:   services.SeverityInfo,
		ActorType:  "user",
		ActorID:    userID,
		TargetType: "plan_order",
		TargetID:   resp.ID,
		Detail: map[string]any{
			"promotion_code": code,
			"discount_cents": reservation.DiscountCents,
		},
	})

	return nil
}

// isFirstPurchase reports whether userID has never had a paid order, which is
// what a first_purchase_only code checks against - a cancelled or expired
// order does not count as a purchase.
func (h *UserOrderHandler) isFirstPurchase(ctx context.Context, userID string) (bool, error) {
	var hasPaid bool
	if err := h.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM plan_orders WHERE user_id = $1 AND status = 'paid')`,
		userID,
	).Scan(&hasPaid); err != nil {
		return false, fmt.Errorf("check first purchase: %w", err)
	}
	return !hasPaid, nil
}

// settleZeroCostOrder grants a plan whose order was discounted to nothing
// without contacting any payment provider - the "provider" here is the panel
// itself, an authenticated channel exactly like AdminProvider's, so it goes
// through the identical PaymentService.Settle state machine (idempotent
// event ledger, order claim, plan grant) rather than a separate code path.
func (h *UserOrderHandler) settleZeroCostOrder(ctx context.Context, orderID, userID string) error {
	_, err := h.payment.Settle(ctx, payments.Confirmation{
		Provider:   "promotion",
		ExternalID: "order:" + orderID,
		OrderID:    orderID,
		ActorType:  "user",
		ActorID:    userID,
	})
	return err
}

// List handles GET /api/v1/user/orders.
// @Summary List my orders
// @Description Returns the authenticated user's plan orders
// @Tags user-orders
// @Produce json
// @Success 200 {array} orderResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/orders [get]
func (h *UserOrderHandler) List(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	rows, err := h.db.Query(context.Background(),
		`SELECT o.id, o.plan_id, p.name, o.duration_days, o.price_cents, o.discount_cents, o.status,
		        o.created_at, o.expires_at, o.provider, o.paid_at, o.cancelled_at
		 FROM plan_orders o
		 JOIN plans p ON p.id = o.plan_id
		 WHERE o.user_id = $1
		 ORDER BY o.created_at DESC`,
		userID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	defer rows.Close()

	results := make([]orderResponse, 0)
	for rows.Next() {
		var r orderResponse
		if err := rows.Scan(&r.ID, &r.PlanID, &r.PlanName, &r.DurationDays, &r.PriceCents, &r.DiscountCents, &r.Status, &r.CreatedAt, &r.ExpiresAt, &r.Provider, &r.PaidAt, &r.CancelledAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
		}
		results = append(results, r)
	}

	return c.JSON(results)
}

// Cancel handles POST /api/v1/user/orders/:id/cancel. Only the order's own
// user can cancel it, and only while it is still pending.
// @Summary Cancel my order
// @Description Cancels one of the authenticated user's own pending orders
// @Tags user-orders
// @Produce json
// @Param id path string true "Order ID"
// @Success 200 {object} orderResponse
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /user/orders/{id}/cancel [post]
func (h *UserOrderHandler) Cancel(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	id := c.Params("id")
	ctx := context.Background()

	tag, err := h.db.Exec(ctx,
		`UPDATE plan_orders SET status = 'cancelled', cancelled_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = 'pending'`,
		id, userID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "order not found, not yours, or already resolved",
		})
	}

	if err := h.promotion.Release(ctx, id); err != nil {
		// The order is already cancelled; a stuck redemption slot is a
		// bookkeeping gap, not a reason to fail the user-facing cancel.
		fmt.Printf("promotions: failed to release order %s on cancel: %v\n", id, err)
	}

	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventOrderCancelled,
		Severity:   services.SeverityInfo,
		ActorType:  "user",
		ActorID:    userID,
		TargetType: "plan_order",
		TargetID:   id,
	})

	return c.JSON(fiber.Map{"message": "order cancelled"})
}

// isPendingOrderConflict reports whether err is the unique-violation from
// idx_plan_orders_one_pending, i.e. the user already has an open order.
func isPendingOrderConflict(err error) bool {
	return strings.Contains(err.Error(), "idx_plan_orders_one_pending")
}

type applyPromotionRequest struct {
	PromotionCode string `json:"promotion_code"`
}

// ApplyPromotion handles PATCH /api/v1/user/orders/:id/promotion, letting a
// user attach or replace a promotion code on their own still-pending order -
// the "apply now or apply later" case createOrderRequest.PromotionCode alone
// cannot cover, since a code the user did not have yet at order creation is
// still usable up until checkout starts. Replacing first releases whatever
// code (if any) is already reserved on the order and restores its original
// price, so re-validating the new code checks it against the undiscounted
// total rather than compounding onto an already-discounted one.
// @Summary Apply a promotion code to my pending order
// @Description Attaches or replaces a promotion code on one of the authenticated user's own pending orders
// @Tags user-orders
// @Accept json
// @Produce json
// @Param id path string true "Order ID"
// @Param body body applyPromotionRequest true "Promotion code to apply"
// @Success 200 {object} orderResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/orders/{id}/promotion [patch]
func (h *UserOrderHandler) ApplyPromotion(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	orderID := c.Params("id")
	ctx := context.Background()

	var req applyPromotionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.PromotionCode == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "promotion_code is required"})
	}

	var resp orderResponse
	var originalCents int64
	err := h.db.QueryRow(ctx,
		`SELECT o.id, o.plan_id, p.name, o.duration_days, o.price_cents + o.discount_cents, o.status
		 FROM plan_orders o
		 JOIN plans p ON p.id = o.plan_id
		 WHERE o.id = $1 AND o.user_id = $2 AND o.status = 'pending'`,
		orderID, userID,
	).Scan(&resp.ID, &resp.PlanID, &resp.PlanName, &resp.DurationDays, &originalCents, &resp.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "order not found, not yours, or already resolved",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	resp.PriceCents = originalCents

	if err := h.promotion.Release(ctx, orderID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE plan_orders SET promotion_id = NULL, discount_cents = 0, price_cents = $2 WHERE id = $1`,
		orderID, originalCents,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	if err := h.applyPromotion(ctx, &resp, userID, req.PromotionCode); err != nil {
		var reject *services.PromotionRejectError
		if errors.As(err, &reject) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "promotion_rejected", "reason": string(reject.Reason),
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	return c.JSON(resp)
}
