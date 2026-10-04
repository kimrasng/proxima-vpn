package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// auditApp serves the audit route behind the same admin locals
// middleware.AdminJWTMiddleware would have set, so the test exercises the real
// HTTP path (params, status codes, JSON encoding) rather than the method alone.
func auditApp(t *testing.T, pool *pgxpool.Pool) *fiber.App {
	t.Helper()

	handler := NewAdminOrderHandler(pool)
	app := fiber.New()
	app.Get("/orders/:id/audit", func(c *fiber.Ctx) error {
		c.Locals("admin_id", "00000000-0000-0000-0000-000000000000")
		c.Locals("email", "auditor@x.test")
		return handler.Audit(c)
	})
	return app
}

// getAuditRaw returns the status and the undecoded body, so a test can assert on
// the wire bytes instead of on a struct that could silently drop a field.
func getAuditRaw(t *testing.T, app *fiber.App, orderID string) (int, []byte) {
	t.Helper()

	response, err := app.Test(httptest.NewRequest("GET", "/orders/"+orderID+"/audit", nil))
	if err != nil {
		t.Fatalf("audit request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read audit body: %v", err)
	}
	return response.StatusCode, body
}

func getAudit(t *testing.T, app *fiber.App, orderID string) (adminOrderAuditResponse, int) {
	t.Helper()

	status, body := getAuditRaw(t, app, orderID)
	var audit adminOrderAuditResponse
	if status == fiber.StatusOK {
		if err := json.Unmarshal(body, &audit); err != nil {
			t.Fatalf("decode audit response: %v (body %s)", err, body)
		}
	}
	return audit, status
}

// seedAuditOrder creates one paid order carrying full request context and grant
// evidence, plus two payment events received an hour apart.
func seedAuditOrder(t *testing.T, pool *pgxpool.Pool, userID, planID, suffix string) string {
	t.Helper()
	ctx := context.Background()

	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders
		   (user_id, plan_id, duration_days, price_cents, discount_cents, status,
		    paid_at, paid_by, provider, provider_session_id,
		    origin, client_ip, user_agent, browser_family, os_family, locale, device_fingerprint,
		    granted_plan_expires_before, granted_plan_expires_after)
		 VALUES ($1, $2, 30, 1000, 250, 'paid',
		         NOW(), 'auditor@x.test', 'stripe', 'cs_test_'||$3,
		         'audit.example.test', '203.0.113.7', $4, 'chrome', 'macos', 'ko-KR', 'fp-audit-'||$3,
		         NOW() - INTERVAL '30 days', NOW() + INTERVAL '30 days')
		 RETURNING id::text`,
		userID, planID, suffix, testCreateUA,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed audit order: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_events WHERE order_id = $1`, orderID)
	})
	return orderID
}

// seedAuditPaymentEvent writes one payment_events row whose payload carries an
// obvious PII marker, so any leak of the raw provider body is detectable.
func seedAuditPaymentEvent(t *testing.T, pool *pgxpool.Pool, orderID, provider, externalID, outcome string, receivedAgo time.Duration) {
	t.Helper()

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO payment_events
		   (provider, external_id, order_id, amount_cents, currency, session_id,
		    outcome, reason, payload, request_ip, user_agent, request_id,
		    actor_type, actor_id, received_at, processed_at)
		 VALUES ($1, $2, $3, 1000, 'usd', 'cs_'||$2,
		         $4, 'seeded', $5::jsonb, '198.51.100.9', $6, 'req-'||$2,
		         'webhook', 'evt-actor', NOW() - $7::interval, NOW())`,
		provider, externalID, orderID, outcome,
		`{"customer_email":"`+auditPayloadMarker+`","card_last4":"4242"}`,
		testCheckoutUA, receivedAgo.String(),
	); err != nil {
		t.Fatalf("seed payment event %s: %v", externalID, err)
	}
}

// auditPayloadMarker is the PII sentinel planted in every seeded payment
// payload. Its absence from a response is what proves the payload never ships.
const auditPayloadMarker = "payload-pii-must-never-ship@x.test"

func TestAudit_ReturnsOrderRequestContextGrantAndOrderedEvents(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("audit-ok-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	orderID := seedAuditOrder(t, pool, userID, planID, suffix)

	// Inserted oldest-first so a handler that forgot ORDER BY would return the
	// insertion order and fail the received_at DESC assertion below.
	seedAuditPaymentEvent(t, pool, orderID, "stripe", "evt_old_"+suffix, "failed", 2*time.Hour)
	seedAuditPaymentEvent(t, pool, orderID, "promotion", "evt_new_"+suffix, "granted", 1*time.Hour)

	audit, status := getAudit(t, auditApp(t, pool), orderID)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", status, fiber.StatusOK)
	}

	if audit.Order.ID != orderID {
		t.Errorf("order.id = %q, want %q", audit.Order.ID, orderID)
	}
	if audit.Order.UserID != userID {
		t.Errorf("order.user_id = %q, want %q", audit.Order.UserID, userID)
	}
	if audit.Order.UserEmail != "free-order-"+suffix+"@x.test" {
		t.Errorf("order.user_email = %q, want the joined user email", audit.Order.UserEmail)
	}
	if audit.Order.PlanID != planID || audit.Order.PlanName != "free-order-"+suffix {
		t.Errorf("order plan = %q/%q, want the joined plan %q", audit.Order.PlanID, audit.Order.PlanName, planID)
	}
	if audit.Order.Status != "paid" || audit.Order.PriceCents != 1000 || audit.Order.DiscountCents != 250 {
		t.Errorf("order status/price/discount = %q/%d/%d, want paid/1000/250",
			audit.Order.Status, audit.Order.PriceCents, audit.Order.DiscountCents)
	}
	if audit.Order.Provider != "stripe" {
		t.Errorf("order.provider = %q, want stripe", audit.Order.Provider)
	}
	if audit.Order.PaidAt == nil {
		t.Error("order.paid_at is null, want the settlement timestamp")
	}

	if audit.RequestContext == nil {
		t.Fatal("request_context is null, want the captured checkout context")
	}
	if audit.RequestContext.ClientIP != "203.0.113.7" {
		t.Errorf("request_context.client_ip = %q, want 203.0.113.7", audit.RequestContext.ClientIP)
	}
	if audit.RequestContext.BrowserFamily != "chrome" || audit.RequestContext.OSFamily != "macos" {
		t.Errorf("request_context browser/os = %q/%q, want chrome/macos",
			audit.RequestContext.BrowserFamily, audit.RequestContext.OSFamily)
	}
	if audit.RequestContext.Locale != "ko-KR" || audit.RequestContext.Origin != "audit.example.test" {
		t.Errorf("request_context locale/origin = %q/%q, want ko-KR/audit.example.test",
			audit.RequestContext.Locale, audit.RequestContext.Origin)
	}

	if audit.Grant == nil {
		t.Fatal("grant is null, want the recorded before/after expiry evidence")
	}
	if audit.Grant.PlanExpiresBefore == nil || audit.Grant.PlanExpiresAfter == nil {
		t.Fatalf("grant before/after = %v/%v, want both recorded",
			audit.Grant.PlanExpiresBefore, audit.Grant.PlanExpiresAfter)
	}
	if !audit.Grant.PlanExpiresAfter.After(*audit.Grant.PlanExpiresBefore) {
		t.Errorf("grant after %v is not later than before %v", audit.Grant.PlanExpiresAfter, audit.Grant.PlanExpiresBefore)
	}

	if len(audit.PaymentEvents) != 2 {
		t.Fatalf("payment_events length = %d, want 2", len(audit.PaymentEvents))
	}
	if audit.PaymentEvents[0].ExternalID != "evt_new_"+suffix {
		t.Errorf("payment_events[0].external_id = %q, want the newest event", audit.PaymentEvents[0].ExternalID)
	}
	if audit.PaymentEvents[1].ExternalID != "evt_old_"+suffix {
		t.Errorf("payment_events[1].external_id = %q, want the oldest event", audit.PaymentEvents[1].ExternalID)
	}
	if audit.PaymentEvents[0].ReceivedAt.Before(audit.PaymentEvents[1].ReceivedAt) {
		t.Error("payment_events are not ordered received_at DESC")
	}
	// The promotion provider settles a free order but need not exist in the
	// provider registry: the handler must pass the name through untouched.
	if audit.PaymentEvents[0].Provider != "promotion" {
		t.Errorf("payment_events[0].provider = %q, want the opaque promotion name", audit.PaymentEvents[0].Provider)
	}
	if audit.PaymentEvents[0].Outcome != "granted" || audit.PaymentEvents[1].Outcome != "failed" {
		t.Errorf("payment_events outcomes = %q/%q, want granted/failed",
			audit.PaymentEvents[0].Outcome, audit.PaymentEvents[1].Outcome)
	}
	if audit.PaymentEvents[0].AmountCents != 1000 || audit.PaymentEvents[0].Currency != "usd" {
		t.Errorf("payment_events[0] amount/currency = %d/%q, want 1000/usd",
			audit.PaymentEvents[0].AmountCents, audit.PaymentEvents[0].Currency)
	}
}

func TestAudit_MalformedOrderIDIsRejectedAsBadRequest(t *testing.T) {
	pool := testOrderDB(t)
	app := auditApp(t, pool)

	// A non-UUID segment reaching a uuid comparison makes Postgres reject the
	// query, which the handler could only report as 500 for a client mistake.
	// The last case is percent-encoded because a raw space is not a legal
	// request target; it still arrives at the handler as injection text.
	for _, malformed := range []string{"not-a-uuid", "12345", "00000000-0000-0000-0000-00000000000", "%27%20OR%201%3D1--"} {
		status, body := getAuditRaw(t, app, malformed)
		if status != fiber.StatusBadRequest {
			t.Errorf("status for %q = %d, want %d (body %s)", malformed, status, fiber.StatusBadRequest, body)
		}
	}
}

func TestAudit_UnknownOrderIDIsNotFound(t *testing.T) {
	pool := testOrderDB(t)

	status, body := getAuditRaw(t, auditApp(t, pool), "11111111-2222-3333-4444-555555555555")
	if status != fiber.StatusNotFound {
		t.Fatalf("status = %d, want %d (body %s)", status, fiber.StatusNotFound, body)
	}
}

func TestAudit_PendingOrderHasNoGrantAndANonNullEmptyEventList(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("audit-pending-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)

	// A pending order never granted and never confirmed: no grant evidence and
	// no payment events exist yet, which is a normal state, not an error.
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status)
		 VALUES ($1, $2, 30, 1000, 'pending') RETURNING id::text`,
		userID, planID,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed pending order: %v", err)
	}

	audit, status := getAudit(t, auditApp(t, pool), orderID)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", status, fiber.StatusOK)
	}
	if audit.Order.Status != "pending" {
		t.Errorf("order.status = %q, want pending", audit.Order.Status)
	}
	if audit.Order.PaidAt != nil {
		t.Errorf("order.paid_at = %v, want null on a pending order", audit.Order.PaidAt)
	}
	if audit.Grant != nil {
		t.Errorf("grant = %+v, want null before any grant ran", audit.Grant)
	}
	// This row predates capture, so every context column is NULL.
	if audit.RequestContext != nil {
		t.Errorf("request_context = %+v, want null when nothing was captured", audit.RequestContext)
	}
	if audit.PaymentEvents == nil {
		t.Error("payment_events is null, want an empty list so the UI can iterate unconditionally")
	}
	if len(audit.PaymentEvents) != 0 {
		t.Errorf("payment_events length = %d, want 0", len(audit.PaymentEvents))
	}

	// The empty collection must be [] on the wire, never null.
	_, body := getAuditRaw(t, auditApp(t, pool), orderID)
	if !strings.Contains(string(body), `"payment_events":[]`) {
		t.Errorf("body does not carry an empty payment_events array: %s", body)
	}
}

// jsonKeysOf walks a response type and collects every JSON key it can emit,
// descending through pointers, slices and nested structs.
func jsonKeysOf(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool, keys map[string]string, path string) {
	t.Helper()

	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return
	}
	seen[typ] = true

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = field.Name
		}
		keys[tag] = path + "." + field.Name
		jsonKeysOf(t, field.Type, seen, keys, path+"."+field.Name)
	}
}

func TestAudit_ResponseTypesCanNeverEmitARawPaymentPayload(t *testing.T) {
	keys := map[string]string{}
	jsonKeysOf(t, reflect.TypeOf(adminOrderAuditResponse{}), map[reflect.Type]bool{}, keys, "adminOrderAuditResponse")

	if len(keys) == 0 {
		t.Fatal("walked no JSON keys; the structural guard would pass vacuously")
	}
	// payment_events.payload is a raw provider body that may carry cardholder
	// or contact PII, so no struct in the audit tree may be able to encode it.
	for _, forbidden := range []string{"payload", "raw_payload", "event_payload"} {
		if where, found := keys[forbidden]; found {
			t.Errorf("audit response can emit a %q JSON key at %s; the raw provider payload must be excluded entirely", forbidden, where)
		}
	}
}

func TestAudit_ResponseBodyOmitsThePaymentPayloadAndItsContents(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("audit-nopayload-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	orderID := seedAuditOrder(t, pool, userID, planID, suffix)
	seedAuditPaymentEvent(t, pool, orderID, "stripe", "evt_pii_"+suffix, "granted", time.Minute)

	status, body := getAuditRaw(t, auditApp(t, pool), orderID)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", status, fiber.StatusOK, body)
	}

	if strings.Contains(string(body), auditPayloadMarker) {
		t.Errorf("response body leaked the seeded payload PII marker: %s", body)
	}
	if strings.Contains(string(body), "card_last4") || strings.Contains(string(body), "4242") {
		t.Errorf("response body leaked payload card details: %s", body)
	}
	if strings.Contains(string(body), `"payload"`) {
		t.Errorf("response body carries a payload key: %s", body)
	}
}

func TestList_StaysABackwardCompatibleSupersetWithContextFields(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("audit-list-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	orderID := seedAuditOrder(t, pool, userID, planID, suffix)

	handler := NewAdminOrderHandler(pool)
	app := fiber.New()
	app.Get("/orders", func(c *fiber.Ctx) error {
		c.Locals("admin_id", "00000000-0000-0000-0000-000000000000")
		return handler.List(c)
	})

	response, err := app.Test(httptest.NewRequest("GET", "/orders?status=paid", nil))
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}

	var rows []map[string]any
	if err := json.NewDecoder(response.Body).Decode(&rows); err != nil {
		t.Fatalf("decode list response: %v", err)
	}

	var row map[string]any
	for _, candidate := range rows {
		if candidate["id"] == orderID {
			row = candidate
			break
		}
	}
	if row == nil {
		t.Fatalf("seeded order %s missing from the paid list", orderID)
	}

	// Every key the existing admin UI already reads, with the type it reads it
	// as: the audit work may only add fields, never rename or retype one.
	for key, wantKind := range map[string]reflect.Kind{
		"id":            reflect.String,
		"user_email":    reflect.String,
		"user_name":     reflect.String,
		"plan_id":       reflect.String,
		"plan_name":     reflect.String,
		"duration_days": reflect.Float64,
		"price_cents":   reflect.Float64,
		"status":        reflect.String,
		"created_at":    reflect.String,
		"paid_at":       reflect.String,
		"paid_by":       reflect.String,
	} {
		value, present := row[key]
		if !present {
			t.Errorf("list row lost the %q key", key)
			continue
		}
		if got := reflect.ValueOf(value).Kind(); got != wantKind {
			t.Errorf("list row %q is %v, want %v", key, got, wantKind)
		}
	}

	// The new optional context summary fields the audit UI reads from the list.
	if row["client_ip"] != "203.0.113.7" {
		t.Errorf("list row client_ip = %v, want 203.0.113.7", row["client_ip"])
	}
	if row["browser_family"] != "chrome" || row["os_family"] != "macos" {
		t.Errorf("list row browser/os = %v/%v, want chrome/macos", row["browser_family"], row["os_family"])
	}
	if _, leaked := row["payload"]; leaked {
		t.Error("list row carries a payload key")
	}
}
