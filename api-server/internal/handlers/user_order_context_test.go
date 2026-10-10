package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// storedContext is the audit context of one order as it sits in the table.
// Every field is a pointer because NULL ("never captured") is a distinct state
// from an empty string, and the non-overwrite rule is expressed in those terms.
type storedContext struct {
	Origin            *string
	ClientIP          *string
	UserAgent         *string
	BrowserFamily     *string
	OSFamily          *string
	Locale            *string
	DeviceFingerprint *string
}

func readStoredContext(t *testing.T, pool *pgxpool.Pool, orderID string) storedContext {
	t.Helper()

	var got storedContext
	if err := pool.QueryRow(context.Background(),
		`SELECT origin, client_ip, user_agent, browser_family, os_family, locale, device_fingerprint
		 FROM plan_orders WHERE id = $1`,
		orderID,
	).Scan(&got.Origin, &got.ClientIP, &got.UserAgent, &got.BrowserFamily, &got.OSFamily, &got.Locale, &got.DeviceFingerprint); err != nil {
		t.Fatalf("read order context: %v", err)
	}
	return got
}

func deref(t *testing.T, label string, value *string) string {
	t.Helper()
	if value == nil {
		t.Fatalf("%s is NULL, want a captured value", label)
	}
	return *value
}

// createOrderApp posts one create-order request carrying headers and returns
// the decoded order plus the HTTP status.
func createOrderApp(t *testing.T, pool *pgxpool.Pool, userID string, body []byte, headers map[string]string) (orderResponse, int) {
	t.Helper()

	handler := NewUserOrderHandler(pool, config.PaymentsConfig{PendingTTLRaw: "1h"})
	app := fiber.New()
	app.Post("/orders", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return handler.Create(c)
	})

	request := httptest.NewRequest("POST", "/orders", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("create order request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	var order orderResponse
	if response.StatusCode == fiber.StatusCreated {
		if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
	}
	return order, response.StatusCode
}

// startCheckoutApp starts a checkout for orderID through the admin provider,
// which reaches nothing external, carrying headers of its own.
func startCheckoutApp(t *testing.T, pool *pgxpool.Pool, userID, orderID string, headers map[string]string) int {
	t.Helper()

	handler := NewUserCheckoutHandler(pool, payments.Registry{payments.ProviderAdmin: payments.NewAdminProvider()})
	app := fiber.New()
	app.Post("/orders/:id/checkout", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return handler.Start(c)
	})

	body, err := json.Marshal(startCheckoutRequest{Provider: payments.ProviderAdmin})
	if err != nil {
		t.Fatalf("marshal checkout request: %v", err)
	}
	request := httptest.NewRequest("POST", "/orders/"+orderID+"/checkout", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("start checkout request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	return response.StatusCode
}

const (
	testCreateUA     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0 Safari/537.36"
	testCheckoutUA   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0"
	testCreateOrigin = "https://create.example.test"
	testStartOrigin  = "https://checkout.example.test"
)

func TestCreate_PersistsEveryRequestContextField(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("ctx-create-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)

	body, err := json.Marshal(createOrderRequest{
		PlanID:            planID,
		DurationDays:      30,
		DeviceFingerprint: "fp-create-" + suffix,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	order, status := createOrderApp(t, pool, userID, body, map[string]string{
		"Origin":          testCreateOrigin + "/checkout?step=1",
		"User-Agent":      testCreateUA,
		"Accept-Language": "ko-KR,ko;q=0.9,en;q=0.8",
	})
	if status != fiber.StatusCreated {
		t.Fatalf("status = %d, want %d", status, fiber.StatusCreated)
	}

	got := readStoredContext(t, pool, order.ID)
	if origin := deref(t, "origin", got.Origin); origin != "create.example.test" {
		t.Errorf("origin = %q, want %q", origin, "create.example.test")
	}
	if ip := deref(t, "client_ip", got.ClientIP); ip == "" {
		t.Error("client_ip is empty, want the request peer")
	}
	if ua := deref(t, "user_agent", got.UserAgent); ua != testCreateUA {
		t.Errorf("user_agent = %q, want %q", ua, testCreateUA)
	}
	if browser := deref(t, "browser_family", got.BrowserFamily); browser != "chrome" {
		t.Errorf("browser_family = %q, want %q", browser, "chrome")
	}
	if osFamily := deref(t, "os_family", got.OSFamily); osFamily != "macos" {
		t.Errorf("os_family = %q, want %q", osFamily, "macos")
	}
	if locale := deref(t, "locale", got.Locale); locale != "ko-KR" {
		t.Errorf("locale = %q, want %q", locale, "ko-KR")
	}
	if fp := deref(t, "device_fingerprint", got.DeviceFingerprint); fp != "fp-create-"+suffix {
		t.Errorf("device_fingerprint = %q, want %q", fp, "fp-create-"+suffix)
	}
}

func TestStart_FillsOnlyNullContextAndKeepsCreateTimeValues(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("ctx-keep-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)

	body, err := json.Marshal(createOrderRequest{
		PlanID:            planID,
		DurationDays:      30,
		DeviceFingerprint: "fp-keep-" + suffix,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	order, status := createOrderApp(t, pool, userID, body, map[string]string{
		"Origin":          testCreateOrigin,
		"User-Agent":      testCreateUA,
		"Accept-Language": "ko-KR,ko;q=0.9",
	})
	if status != fiber.StatusCreated {
		t.Fatalf("create status = %d, want %d", status, fiber.StatusCreated)
	}
	beforeStart := readStoredContext(t, pool, order.ID)

	// Every checkout-time value differs from what create time stored, so an
	// overwrite is visible rather than masked by an identical fixture.
	if status := startCheckoutApp(t, pool, userID, order.ID, map[string]string{
		"Origin":          testStartOrigin,
		"User-Agent":      testCheckoutUA,
		"Accept-Language": "de-DE,de;q=0.9",
	}); status != fiber.StatusOK {
		t.Fatalf("checkout status = %d, want %d", status, fiber.StatusOK)
	}

	afterStart := readStoredContext(t, pool, order.ID)
	if got, want := deref(t, "origin", afterStart.Origin), deref(t, "origin", beforeStart.Origin); got != want {
		t.Errorf("origin = %q after checkout, want the create-time %q", got, want)
	}
	if got, want := deref(t, "user_agent", afterStart.UserAgent), deref(t, "user_agent", beforeStart.UserAgent); got != want {
		t.Errorf("user_agent = %q after checkout, want the create-time %q", got, want)
	}
	if got, want := deref(t, "browser_family", afterStart.BrowserFamily), deref(t, "browser_family", beforeStart.BrowserFamily); got != want {
		t.Errorf("browser_family = %q after checkout, want the create-time %q", got, want)
	}
	if got, want := deref(t, "os_family", afterStart.OSFamily), deref(t, "os_family", beforeStart.OSFamily); got != want {
		t.Errorf("os_family = %q after checkout, want the create-time %q", got, want)
	}
	if got, want := deref(t, "locale", afterStart.Locale), deref(t, "locale", beforeStart.Locale); got != want {
		t.Errorf("locale = %q after checkout, want the create-time %q", got, want)
	}
	if got, want := deref(t, "device_fingerprint", afterStart.DeviceFingerprint), deref(t, "device_fingerprint", beforeStart.DeviceFingerprint); got != want {
		t.Errorf("device_fingerprint = %q after checkout, want the create-time %q", got, want)
	}
}

func TestStart_FillsContextThatWasNeverCaptured(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("ctx-fill-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)

	// An order row from before capture existed: every audit field is NULL.
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status)
		 VALUES ($1, $2, 30, 1000, 'pending') RETURNING id::text`,
		userID, planID,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	if status := startCheckoutApp(t, pool, userID, orderID, map[string]string{
		"Origin":          testStartOrigin,
		"User-Agent":      testCheckoutUA,
		"Accept-Language": "de-DE,de;q=0.9",
	}); status != fiber.StatusOK {
		t.Fatalf("checkout status = %d, want %d", status, fiber.StatusOK)
	}

	got := readStoredContext(t, pool, orderID)
	if origin := deref(t, "origin", got.Origin); origin != "checkout.example.test" {
		t.Errorf("origin = %q, want %q", origin, "checkout.example.test")
	}
	if ua := deref(t, "user_agent", got.UserAgent); ua != testCheckoutUA {
		t.Errorf("user_agent = %q, want %q", ua, testCheckoutUA)
	}
	if browser := deref(t, "browser_family", got.BrowserFamily); browser != "firefox" {
		t.Errorf("browser_family = %q, want %q", browser, "firefox")
	}
	if osFamily := deref(t, "os_family", got.OSFamily); osFamily != "windows" {
		t.Errorf("os_family = %q, want %q", osFamily, "windows")
	}
	if locale := deref(t, "locale", got.Locale); locale != "de-DE" {
		t.Errorf("locale = %q, want %q", locale, "de-DE")
	}
	if ip := deref(t, "client_ip", got.ClientIP); ip == "" {
		t.Error("client_ip is empty, want the request peer")
	}
}

func TestCreate_RepeatedFingerprintNeverGatesAnOrder(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("ctx-fp-%d", os.Getpid())
	firstUser, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	secondUser, secondPlanID, _ := seedFreeOrderData(t, pool, ctx, suffix+"-second")

	// One fingerprint reused across an earlier order of the same user and
	// across a different user: neither is an eligibility or uniqueness input.
	fingerprint := "fp-shared-" + suffix
	firstBody, err := json.Marshal(createOrderRequest{PlanID: planID, DurationDays: 30, DeviceFingerprint: fingerprint})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	first, status := createOrderApp(t, pool, firstUser, firstBody, map[string]string{"User-Agent": testCreateUA})
	if status != fiber.StatusCreated {
		t.Fatalf("first create status = %d, want %d", status, fiber.StatusCreated)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE plan_orders SET status = 'cancelled', cancelled_at = NOW() WHERE id = $1`, first.ID,
	); err != nil {
		t.Fatalf("cancel first order: %v", err)
	}

	if _, status := createOrderApp(t, pool, firstUser, firstBody, map[string]string{"User-Agent": testCreateUA}); status != fiber.StatusCreated {
		t.Errorf("same-user repeat status = %d, want %d: the fingerprint gated a second order", status, fiber.StatusCreated)
	}

	secondBody, err := json.Marshal(createOrderRequest{PlanID: secondPlanID, DurationDays: 30, DeviceFingerprint: fingerprint})
	if err != nil {
		t.Fatalf("marshal second request: %v", err)
	}
	if _, status := createOrderApp(t, pool, secondUser, secondBody, map[string]string{"User-Agent": testCreateUA}); status != fiber.StatusCreated {
		t.Errorf("other-user status = %d, want %d: the shared fingerprint gated another user", status, fiber.StatusCreated)
	}
}

func TestCreate_MissingContextHeadersStillCreatesTheOrder(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("ctx-bare-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)

	body, err := json.Marshal(createOrderRequest{PlanID: planID, DurationDays: 30})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	// No Origin, no Referer, no User-Agent, no Accept-Language, no fingerprint:
	// Referrer-Policy makes Referer unreliable, so absent context is normal and
	// must never turn a valid order into an error.
	order, status := createOrderApp(t, pool, userID, body, map[string]string{})
	if status != fiber.StatusCreated {
		t.Fatalf("status = %d, want %d", status, fiber.StatusCreated)
	}

	got := readStoredContext(t, pool, order.ID)
	if origin := deref(t, "origin", got.Origin); origin != "" {
		t.Errorf("origin = %q, want empty with neither Origin nor Referer sent", origin)
	}
	if fp := deref(t, "device_fingerprint", got.DeviceFingerprint); fp != "" {
		t.Errorf("device_fingerprint = %q, want empty when the payload omits it", fp)
	}
}
