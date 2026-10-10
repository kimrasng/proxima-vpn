package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func testOrderDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed order handler tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedFreeOrderData(t *testing.T, pool *pgxpool.Pool, ctx context.Context, suffix string) (string, string, string) {
	t.Helper()

	var groupID, planID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, "free-order-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed node group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id, advertise, is_advertised)
		 VALUES ($1, 30, 1, $2, true, true) RETURNING id::text`,
		"free-order-"+suffix, groupID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_prices (plan_id, duration_days, price_cents) VALUES ($1, 30, 1000)`, planID); err != nil {
		t.Fatalf("seed plan price: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, sub_token, status)
		 VALUES ($1, 'x', $2, 'pending') RETURNING id::text`,
		"free-order-"+suffix+"@x.test", "free-order-token-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM promotion_redemptions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plan_orders WHERE user_id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, planID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM node_groups WHERE id = $1`, groupID)
	})
	return userID, planID, groupID
}

func TestCreate_FreePromotionSettlesWithoutExternalProvider(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	// The pid alone repeats under -count and across reruns, and cleanup cannot
	// delete a code still referenced by the settled order, so make it unique.
	code := fmt.Sprintf("FREE-%s-%d", suffix, time.Now().UnixNano())
	now := time.Now()
	if _, err := pool.Exec(ctx,
		`INSERT INTO promotion_codes
		 (code, discount_type, discount_value, valid_from, valid_until, max_redemptions_per_user)
		 VALUES ($1, 'percent', 100, $2, $3, 1)`,
		code, now.Add(-time.Hour), now.Add(time.Hour),
	); err != nil {
		t.Fatalf("seed promotion: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM promotion_codes WHERE code = $1`, code) })
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM promotion_redemptions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM promotion_codes WHERE code = $1`, code)
	})

	handler := NewUserOrderHandler(pool, config.PaymentsConfig{PendingTTLRaw: "1h"})
	app := fiber.New()
	app.Post("/orders", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return handler.Create(c)
	})

	body, err := json.Marshal(createOrderRequest{PlanID: planID, DurationDays: 30, PromotionCode: code})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest("POST", "/orders", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("create order request: %v", err)
	}
	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusCreated)
	}
	defer func() { _ = response.Body.Close() }()

	var order orderResponse
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if order.PriceCents != 0 || order.Status != "paid" {
		t.Fatalf("order price/status = %d/%q, want 0/paid", order.PriceCents, order.Status)
	}

	var status, redemptionStatus, provider string
	var planExpiresAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT o.status, r.status, e.provider, u.plan_expires_at
		 FROM plan_orders o
		 JOIN promotion_redemptions r ON r.order_id = o.id
		 JOIN payment_events e ON e.order_id = o.id
		 JOIN users u ON u.id = o.user_id
		 WHERE o.id = $1`,
		order.ID,
	).Scan(&status, &redemptionStatus, &provider, &planExpiresAt); err != nil {
		t.Fatalf("read settled order: %v", err)
	}
	if status != "paid" || redemptionStatus != "confirmed" || provider != "promotion" || planExpiresAt == nil {
		t.Errorf("settled state = status=%q redemption=%q provider=%q expiry=%v, want paid/confirmed/promotion/non-nil", status, redemptionStatus, provider, planExpiresAt)
	}
}

func TestAdminCancel_ReleasesPromotionReservation(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("admin-cancel-%d", os.Getpid())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status)
		 VALUES ($1, $2, 30, 1000, 'pending') RETURNING id::text`,
		userID, planID,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	code := "ADMIN-CANCEL-" + suffix
	now := time.Now()
	if _, err := pool.Exec(ctx,
		`INSERT INTO promotion_codes
		 (code, discount_type, discount_value, valid_from, valid_until, max_redemptions_per_user)
		 VALUES ($1, 'fixed', 100, $2, $3, 1)`,
		code, now.Add(-time.Hour), now.Add(time.Hour),
	); err != nil {
		t.Fatalf("seed promotion: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM promotion_redemptions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM promotion_codes WHERE code = $1`, code)
	})
	if _, err := services.NewPromotionService(pool).Reserve(ctx, code, userID, orderID, planID, 30, 1000, true); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	handler := NewAdminOrderHandler(pool)
	app := fiber.New()
	app.Post("/orders/:id/cancel", func(c *fiber.Ctx) error {
		c.Locals("admin_id", "00000000-0000-0000-0000-000000000000")
		return handler.Cancel(c)
	})
	response, err := app.Test(httptest.NewRequest("POST", "/orders/"+orderID+"/cancel", nil))
	if err != nil {
		t.Fatalf("cancel order request: %v", err)
	}
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}
	defer func() { _ = response.Body.Close() }()

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM promotion_redemptions WHERE order_id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read redemption: %v", err)
	}
	if status != "released" {
		t.Errorf("redemption status = %q, want released", status)
	}
}

func TestCreate_ReadsPriceAfterConcurrentPlanEdit(t *testing.T) {
	pool := testOrderDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, fmt.Sprintf("price-lock-%d", os.Getpid()))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM plans WHERE id=$1 FOR UPDATE`, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM plan_prices WHERE plan_id=$1`, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO plan_prices (plan_id,duration_days,price_cents) VALUES ($1,30,2500)`, planID); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/orders", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return NewUserOrderHandler(pool, config.PaymentsConfig{PendingTTLRaw: "1h"}).Create(c)
	})
	type result struct {
		status int
		order  orderResponse
		err    error
	}
	finished := make(chan result, 1)
	go func() {
		body := fmt.Sprintf(`{"plan_id":%q,"duration_days":30}`, planID)
		req := httptest.NewRequest("POST", "/orders", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req, -1)
		if err != nil {
			finished <- result{err: err}
			return
		}
		defer func() { _ = res.Body.Close() }()
		var order orderResponse
		err = json.NewDecoder(res.Body).Decode(&order)
		finished <- result{res.StatusCode, order, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM plans%' AND query LIKE '%FOR SHARE%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("order did not wait for the concurrent plan edit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-finished:
		if got.err != nil || got.status != 201 || got.order.PriceCents != 2500 {
			t.Fatalf("order after price edit: %+v; want 201 and current price 2500", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("order did not finish after plan edit committed")
	}
}
