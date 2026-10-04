package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

// adminOrderAuditResponse is the full review record of one plan order: what was
// ordered, where the order came from, what the grant actually moved, and every
// confirmation attempt the order received.
//
// request_context and grant are nullable rather than zeroed, because "never
// captured" and "captured as empty" are different answers to an administrator
// investigating a disputed charge - a pre-capture order and an order placed by a
// client that sent no headers must not look alike. payment_events is always a
// list, never null, so the UI can iterate without a nil check.
type adminOrderAuditResponse struct {
	Order          adminOrderAuditOrder    `json:"order"`
	RequestContext *adminOrderAuditContext `json:"request_context"`
	Grant          *adminOrderAuditGrant   `json:"grant"`
	PaymentEvents  []adminOrderAuditEvent  `json:"payment_events"`
}

// adminOrderAuditOrder is the order itself joined to the user and plan it names.
// duration_days and price_cents are the order's own snapshots, not the plan's
// current pricing, so re-pricing a plan never rewrites this record.
type adminOrderAuditOrder struct {
	ID                string     `json:"id"`
	UserID            string     `json:"user_id"`
	UserEmail         string     `json:"user_email"`
	UserName          string     `json:"user_name"`
	PlanID            string     `json:"plan_id"`
	PlanName          string     `json:"plan_name"`
	DurationDays      int        `json:"duration_days"`
	PriceCents        int64      `json:"price_cents"`
	DiscountCents     int64      `json:"discount_cents"`
	Status            string     `json:"status"`
	Provider          string     `json:"provider"`
	ProviderSessionID string     `json:"provider_session_id"`
	CreatedAt         time.Time  `json:"created_at"`
	PaidAt            *time.Time `json:"paid_at"`
	PaidBy            string     `json:"paid_by"`
	CancelledAt       *time.Time `json:"cancelled_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	ExpiredAt         *time.Time `json:"expired_at"`
}

// adminOrderAuditContext is the bounded checkout context captured with the
// order. None of it is an authorization, eligibility or uniqueness input - it
// answers "where did this order come from", nothing more.
type adminOrderAuditContext struct {
	Origin            string `json:"origin"`
	ClientIP          string `json:"client_ip"`
	UserAgent         string `json:"user_agent"`
	BrowserFamily     string `json:"browser_family"`
	OSFamily          string `json:"os_family"`
	Locale            string `json:"locale"`
	DeviceFingerprint string `json:"device_fingerprint"`
}

// adminOrderAuditGrant is the plan expiry on either side of the grant this
// order paid for, which is what makes an extension auditable after the fact.
// Null until a grant has actually run.
type adminOrderAuditGrant struct {
	PlanExpiresBefore *time.Time `json:"plan_expires_before"`
	PlanExpiresAfter  *time.Time `json:"plan_expires_after"`
}

// adminOrderAuditEvent is one confirmation attempt against the order.
//
// payment_events.payload is deliberately absent from this struct and from the
// query that fills it: the raw provider body can carry cardholder and contact
// PII that an order review never needs, and a field that does not exist cannot
// be leaked by a later careless change. Provider is passed through as an opaque
// name - "promotion" settles a free order without appearing in the provider
// registry at all, so this must never be validated against it.
type adminOrderAuditEvent struct {
	ID          string     `json:"id"`
	Provider    string     `json:"provider"`
	ExternalID  string     `json:"external_id"`
	AmountCents int64      `json:"amount_cents"`
	Currency    string     `json:"currency"`
	SessionID   string     `json:"session_id"`
	Outcome     string     `json:"outcome"`
	Reason      string     `json:"reason"`
	RequestIP   string     `json:"request_ip"`
	UserAgent   string     `json:"user_agent"`
	RequestID   string     `json:"request_id"`
	ActorType   string     `json:"actor_type"`
	ActorID     string     `json:"actor_id"`
	ReceivedAt  time.Time  `json:"received_at"`
	ProcessedAt *time.Time `json:"processed_at"`
}

// Audit handles GET /api/v1/admin/orders/:id/audit.
//
// Two queries rather than one join: an order has many payment events, so a
// single joined read would repeat every order column per event and force the
// handler to de-duplicate rows it already knows are one order.
// @Summary Get plan order audit record
// @Description Returns one order with its user and plan, the checkout request context captured with it, the plan expiry on either side of the grant, and every payment confirmation attempt newest-first. Raw provider payloads are never included.
// @Tags admin-orders
// @Produce json
// @Param id path string true "Order ID"
// @Success 200 {object} adminOrderAuditResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/orders/{id}/audit [get]
func (h *AdminOrderHandler) Audit(c *fiber.Ctx) error {
	id := c.Params("id")
	// Reuses the same guard as the other id-taking admin reads: handing a
	// non-UUID segment to a uuid comparison makes Postgres reject the query,
	// which could only be reported as a 500 for a malformed request.
	if !isUUID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid order id"})
	}

	ctx := context.Background()
	audit, err := h.auditRecord(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "order not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	return c.JSON(audit)
}

// auditRecord reads the order then its events, returning pgx.ErrNoRows when the
// order itself does not exist. An order with no events is not an error.
func (h *AdminOrderHandler) auditRecord(ctx context.Context, id string) (adminOrderAuditResponse, error) {
	var audit adminOrderAuditResponse

	// Nullable text columns scan into pointers so a purged or never-captured
	// context stays distinguishable from one captured as empty.
	var origin, clientIP, userAgent, browserFamily, osFamily, locale, fingerprint *string
	var expiresBefore, expiresAfter *time.Time

	order := &audit.Order
	if err := h.db.QueryRow(ctx,
		`SELECT o.id::text, o.user_id::text, u.email, u.name, o.plan_id::text, p.name,
		        o.duration_days, o.price_cents, o.discount_cents, o.status,
		        o.provider, o.provider_session_id,
		        o.created_at, o.paid_at, o.paid_by, o.cancelled_at, o.expires_at, o.expired_at,
		        o.origin, o.client_ip, o.user_agent, o.browser_family, o.os_family, o.locale,
		        o.device_fingerprint,
		        o.granted_plan_expires_before, o.granted_plan_expires_after
		 FROM plan_orders o
		 JOIN users u ON u.id = o.user_id
		 JOIN plans p ON p.id = o.plan_id
		 WHERE o.id = $1`,
		id,
	).Scan(&order.ID, &order.UserID, &order.UserEmail, &order.UserName, &order.PlanID, &order.PlanName,
		&order.DurationDays, &order.PriceCents, &order.DiscountCents, &order.Status,
		&order.Provider, &order.ProviderSessionID,
		&order.CreatedAt, &order.PaidAt, &order.PaidBy, &order.CancelledAt, &order.ExpiresAt, &order.ExpiredAt,
		&origin, &clientIP, &userAgent, &browserFamily, &osFamily, &locale,
		&fingerprint,
		&expiresBefore, &expiresAfter,
	); err != nil {
		return audit, err
	}

	audit.RequestContext = auditContext(origin, clientIP, userAgent, browserFamily, osFamily, locale, fingerprint)
	if expiresBefore != nil || expiresAfter != nil {
		audit.Grant = &adminOrderAuditGrant{PlanExpiresBefore: expiresBefore, PlanExpiresAfter: expiresAfter}
	}

	events, err := h.auditEvents(ctx, id)
	if err != nil {
		return audit, err
	}
	audit.PaymentEvents = events
	return audit, nil
}

// auditEvents lists the order's confirmation attempts newest-first, matching the
// idx_payment_events_order index. id breaks ties so two events stamped in the
// same transaction still come back in a stable order. Never selects payload.
func (h *AdminOrderHandler) auditEvents(ctx context.Context, orderID string) ([]adminOrderAuditEvent, error) {
	rows, err := h.db.Query(ctx,
		`SELECT id::text, provider, external_id, amount_cents, currency, session_id,
		        outcome, reason, request_ip, user_agent, request_id,
		        actor_type, actor_id, received_at, processed_at
		 FROM payment_events
		 WHERE order_id = $1
		 ORDER BY received_at DESC, id DESC`,
		orderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]adminOrderAuditEvent, 0)
	for rows.Next() {
		var e adminOrderAuditEvent
		if err := rows.Scan(&e.ID, &e.Provider, &e.ExternalID, &e.AmountCents, &e.Currency, &e.SessionID,
			&e.Outcome, &e.Reason, &e.RequestIP, &e.UserAgent, &e.RequestID,
			&e.ActorType, &e.ActorID, &e.ReceivedAt, &e.ProcessedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// auditContext collapses the seven nullable context columns into one nullable
// object: all-NULL means nothing was ever captured for this order, so the whole
// object is absent rather than reported as a row of empty strings.
func auditContext(origin, clientIP, userAgent, browserFamily, osFamily, locale, fingerprint *string) *adminOrderAuditContext {
	columns := []*string{origin, clientIP, userAgent, browserFamily, osFamily, locale, fingerprint}
	captured := false
	for _, column := range columns {
		if column != nil {
			captured = true
			break
		}
	}
	if !captured {
		return nil
	}

	return &adminOrderAuditContext{
		Origin:            derefOrEmpty(origin),
		ClientIP:          derefOrEmpty(clientIP),
		UserAgent:         derefOrEmpty(userAgent),
		BrowserFamily:     derefOrEmpty(browserFamily),
		OSFamily:          derefOrEmpty(osFamily),
		Locale:            derefOrEmpty(locale),
		DeviceFingerprint: derefOrEmpty(fingerprint),
	}
}

func derefOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
