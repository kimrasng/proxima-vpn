package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type promotionRaceFixture struct {
	pool                    *pgxpool.Pool
	app                     *fiber.App
	userID, planID, orderID string
	suffix                  string
}

func newPromotionRaceFixture(t *testing.T, pool *pgxpool.Pool) *promotionRaceFixture {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("promotion-race-%d", time.Now().UnixNano())
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, suffix)
	f := &promotionRaceFixture{pool: pool, userID: userID, planID: planID, suffix: suffix}
	// This runs before seedFreeOrderData's cleanup. Remove every reference
	// before deleting the codes, even when settlement has added payment events.
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM payment_events WHERE order_id IN (SELECT id FROM plan_orders WHERE user_id = $1)`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_redemptions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `UPDATE plan_orders SET promotion_id = NULL WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_codes WHERE code LIKE $1`, suffix+"-%")
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, status, expires_at)
		 VALUES ($1, $2, 30, 1000, 'pending', NOW() + INTERVAL '1 hour') RETURNING id::text`,
		userID, planID,
	).Scan(&f.orderID); err != nil {
		t.Fatalf("seed promotion race order: %v", err)
	}
	handler := NewUserOrderHandler(pool, config.PaymentsConfig{PendingTTLRaw: "1h"}, payments.Registry{})
	f.app = fiber.New()
	f.app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return c.Next()
	})
	f.app.Patch("/orders/:id/promotion", handler.ApplyPromotion)
	f.app.Post("/orders/:id/cancel", handler.Cancel)
	return f
}

func (f *promotionRaceFixture) code(t *testing.T, name, discountType string, discountValue int64) (string, string) {
	t.Helper()
	code := f.suffix + "-" + name
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO promotion_codes
		 (code, discount_type, discount_value, valid_from, valid_until,
		  min_order_cents, max_redemptions, max_redemptions_per_user)
		 VALUES ($1, $2, $3, NOW() - INTERVAL '1 hour', NOW() + INTERVAL '1 hour', 1000, 1, 1)
		 RETURNING id::text`, code, discountType, discountValue,
	).Scan(&id); err != nil {
		t.Fatalf("seed race promotion: %v", err)
	}
	return code, id
}

type promotionHTTPResult struct {
	status int
	body   []byte
	err    error
}

func (f *promotionRaceFixture) request(method, path, code string) promotionHTTPResult {
	body, err := json.Marshal(applyPromotionRequest{PromotionCode: code})
	if err != nil {
		return promotionHTTPResult{err: err}
	}
	req := httptest.NewRequest(method, "/orders/"+f.orderID+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := f.app.Test(req, -1)
	if err != nil {
		return promotionHTTPResult{err: err}
	}
	defer func() { _ = res.Body.Close() }()
	responseBody, err := io.ReadAll(res.Body)
	return promotionHTTPResult{status: res.StatusCode, body: responseBody, err: err}
}

func requirePromotionHTTP(t *testing.T, got promotionHTTPResult, status int) {
	t.Helper()
	if got.err != nil || got.status != status {
		t.Fatalf("HTTP result = %d %s, err=%v; want %d", got.status, got.body, got.err, status)
	}
}

func startPromotionRequest(t *testing.T, f *promotionRaceFixture, method, path, code string) <-chan promotionHTTPResult {
	t.Helper()
	finished := make(chan promotionHTTPResult, 1)
	go func() { finished <- f.request(method, path, code) }()
	return finished
}

func awaitPromotionRequest(t *testing.T, finished <-chan promotionHTTPResult) promotionHTTPResult {
	t.Helper()
	select {
	case got := <-finished:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("promotion race request did not finish")
		return promotionHTTPResult{}
	}
}

// Match an actual blocking backend, not an elapsed sleep. Each test owns its
// blocker PID, so unrelated DB tests cannot satisfy this synchronization point.
func waitPromotionBlockedBy(t *testing.T, pool *pgxpool.Pool, blocker uint32, queryFragment string) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var pid uint32
		err := pool.QueryRow(ctx,
			`SELECT a.pid FROM pg_stat_activity a
			 WHERE a.datname = current_database() AND a.wait_event_type = 'Lock'
			   AND $1::int = ANY(pg_blocking_pids(a.pid))
			   AND position($2 in a.query) > 0
			 ORDER BY a.pid LIMIT 1`, blocker, queryFragment,
		).Scan(&pid)
		if err == nil {
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("observe blocked promotion backend: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no backend running %q blocked on backend %d", queryFragment, blocker)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func holdPromotionRow(t *testing.T, pool *pgxpool.Pool, promotionID string) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, `SELECT id FROM promotion_codes WHERE id = $1 FOR UPDATE`, promotionID); err != nil {
		t.Fatal(err)
	}
	return tx
}

type promotionRaceSnapshot struct {
	status, promotionID, redemptionID, redemptionPromotionID, redemptionStatus string
	price, discount                                                            int64
	redemptionCount                                                            int
}

func promotionSnapshot(t *testing.T, f *promotionRaceFixture) promotionRaceSnapshot {
	t.Helper()
	var got promotionRaceSnapshot
	if err := f.pool.QueryRow(context.Background(),
		`SELECT o.status, o.price_cents, o.discount_cents, o.promotion_id::text,
		        r.id::text, r.promotion_id::text, r.status,
		        (SELECT count(*) FROM promotion_redemptions WHERE order_id = o.id)
		 FROM plan_orders o JOIN promotion_redemptions r ON r.order_id = o.id WHERE o.id = $1`,
		f.orderID,
	).Scan(&got.status, &got.price, &got.discount, &got.promotionID,
		&got.redemptionID, &got.redemptionPromotionID, &got.redemptionStatus, &got.redemptionCount); err != nil {
		t.Fatalf("read promotion race snapshot: %v", err)
	}
	return got
}

func requirePromotionSnapshot(t *testing.T, f *promotionRaceFixture, status string, price, discount int64, promotionID, redemptionStatus string) promotionRaceSnapshot {
	t.Helper()
	got := promotionSnapshot(t, f)
	if got.status != status || got.price != price || got.discount != discount ||
		got.promotionID != promotionID || got.redemptionPromotionID != promotionID ||
		got.redemptionStatus != redemptionStatus || got.redemptionCount != 1 {
		t.Fatalf("promotion snapshot = %+v; want %s price=%d discount=%d promotion=%s redemption=%s (one row)",
			got, status, price, discount, promotionID, redemptionStatus)
	}
	return got
}

func requirePromotionCounter(t *testing.T, pool *pgxpool.Pool, promotionID string, want int) {
	t.Helper()
	var count, active int
	if err := pool.QueryRow(context.Background(),
		`SELECT redeemed_count, (SELECT count(*) FROM promotion_redemptions
		 WHERE promotion_id = p.id AND status <> 'released') FROM promotion_codes p WHERE id = $1`, promotionID,
	).Scan(&count, &active); err != nil {
		t.Fatal(err)
	}
	if count != want || active != want {
		t.Fatalf("promotion %s counter/active ledger rows = %d/%d; want %d/%d", promotionID, count, active, want, want)
	}
}

func TestApplyPromotion_CancelWaitsForAtomicReservation(t *testing.T) {
	pool := testOrderDB(t)
	f := newPromotionRaceFixture(t, pool)
	code, promotionID := f.code(t, "cancel", "fixed", 100)
	lock := holdPromotionRow(t, pool, promotionID)
	patch := startPromotionRequest(t, f, "PATCH", "/promotion", code)
	patchPID := waitPromotionBlockedBy(t, pool, lock.Conn().PgConn().PID(), "promotion_codes")
	cancel := startPromotionRequest(t, f, "POST", "/cancel", "")
	// Cancel takes the order row lock first, so it waits on the in-flight patch.
	waitPromotionBlockedBy(t, pool, patchPID, "FROM plan_orders WHERE id=$1 AND user_id=$2 FOR UPDATE")
	if err := lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	requirePromotionHTTP(t, awaitPromotionRequest(t, patch), fiber.StatusOK)
	requirePromotionHTTP(t, awaitPromotionRequest(t, cancel), fiber.StatusOK)
	requirePromotionSnapshot(t, f, "cancelled", 900, 100, promotionID, "released")
	requirePromotionCounter(t, pool, promotionID, 0)
}

func TestCancel_PromotionReleaseFailureRollsBack(t *testing.T) {
	pool := testOrderDB(t)
	f := newPromotionRaceFixture(t, pool)
	code, promotionID := f.code(t, "cancel-release-failure", "fixed", 100)
	requirePromotionHTTP(t, f.request("PATCH", "/promotion", code), fiber.StatusOK)
	before := requirePromotionSnapshot(t, f, "pending", 900, 100, promotionID, "reserved")
	ctx := context.Background()

	// Fail the counter update after ReleaseTx has changed the ledger row,
	// proving that cancellation, ledger and counter all roll back together.
	name := pgx.Identifier{fmt.Sprintf("cancel_release_failure_%d", time.Now().UnixNano())}.Sanitize()
	dropTrigger := `DROP TRIGGER IF EXISTS ` + name + ` ON promotion_codes`
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, dropTrigger)
		_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS `+name+`() `)
	})
	if _, err := pool.Exec(ctx, `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN
	   RAISE EXCEPTION 'injected promotion release failure';
	 END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE OF redeemed_count ON promotion_codes
	 FOR EACH ROW WHEN (OLD.id = '%s'::uuid) EXECUTE FUNCTION %s()`, name, promotionID, name)); err != nil {
		t.Fatal(err)
	}

	requirePromotionHTTP(t, f.request("POST", "/cancel", ""), fiber.StatusInternalServerError)
	if after := promotionSnapshot(t, f); after != before {
		t.Fatalf("failed cancellation changed order/ledger: before=%+v after=%+v", before, after)
	}
	requirePromotionCounter(t, pool, promotionID, 1)
	var cancelledAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT cancelled_at FROM plan_orders WHERE id = $1`, f.orderID).Scan(&cancelledAt); err != nil {
		t.Fatal(err)
	}
	if cancelledAt != nil {
		t.Fatalf("failed cancellation left cancelled_at = %v", cancelledAt)
	}

	if _, err := pool.Exec(ctx, dropTrigger); err != nil {
		t.Fatal(err)
	}
	requirePromotionHTTP(t, f.request("POST", "/cancel", ""), fiber.StatusOK)
	requirePromotionSnapshot(t, f, "cancelled", 900, 100, promotionID, "released")
	requirePromotionCounter(t, pool, promotionID, 0)
	requirePromotionHTTP(t, f.request("POST", "/cancel", ""), fiber.StatusConflict)
	requirePromotionCounter(t, pool, promotionID, 0)
}

func TestApplyPromotion_SettleWaitsAndChecksUpdatedPrice(t *testing.T) {
	pool := testOrderDB(t)
	f := newPromotionRaceFixture(t, pool)
	code, promotionID := f.code(t, "settle", "fixed", 100)
	lock := holdPromotionRow(t, pool, promotionID)
	patch := startPromotionRequest(t, f, "PATCH", "/promotion", code)
	patchPID := waitPromotionBlockedBy(t, pool, lock.Conn().PgConn().PID(), "promotion_codes")
	svc := services.NewPaymentService(pool)
	type settlement struct {
		result *services.SettleResult
		err    error
	}
	finished := make(chan settlement, 1)
	oldPrice := payments.Confirmation{Provider: "stripe", ExternalID: f.suffix + "-old-price", OrderID: f.orderID, AmountCents: 1000}
	go func() {
		result, err := svc.Settle(context.Background(), oldPrice)
		finished <- settlement{result, err}
	}()
	// Depending on the FK implementation, settlement waits either while
	// inserting the event's order reference or while claiming the order.
	// Both must be blocked by PATCH's order-owning backend, not the code lock.
	waitPromotionBlockedBy(t, pool, patchPID, "")
	if err := lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	requirePromotionHTTP(t, awaitPromotionRequest(t, patch), fiber.StatusOK)
	select {
	case got := <-finished:
		if got.err != nil || got.result == nil || got.result.Outcome != services.SettleIgnored ||
			got.result.Reason != "amount_mismatch" || got.result.Grant != nil {
			t.Fatalf("old-price settlement = %+v, err=%v; want ignored/amount_mismatch without grant", got.result, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("settlement did not finish after promotion lock was released")
	}
	requirePromotionSnapshot(t, f, "pending", 900, 100, promotionID, "reserved")
	requirePromotionCounter(t, pool, promotionID, 1)
	var expires *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT plan_expires_at FROM users WHERE id = $1`, f.userID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires != nil {
		t.Fatalf("mismatched payment granted entitlement until %v", expires)
	}

	correct := payments.Confirmation{Provider: "stripe", ExternalID: f.suffix + "-correct-price", OrderID: f.orderID, AmountCents: 900}
	granted, err := svc.Settle(context.Background(), correct)
	if err != nil || granted == nil || granted.Outcome != services.SettleGranted || granted.Grant == nil {
		t.Fatalf("correct-price settlement = %+v, err=%v; want granted", granted, err)
	}
	requirePromotionSnapshot(t, f, "paid", 900, 100, promotionID, "confirmed")
	var afterGrant time.Time
	if err := pool.QueryRow(context.Background(), `SELECT plan_expires_at FROM users WHERE id = $1`, f.userID).Scan(&afterGrant); err != nil {
		t.Fatal(err)
	}
	for i, outcome := range []services.SettleOutcome{services.SettleDuplicate, services.SettleIgnored} {
		if i == 1 {
			correct.ExternalID += "-distinct"
		}
		got, err := svc.Settle(context.Background(), correct)
		if err != nil || got == nil || got.Outcome != outcome || got.Grant != nil {
			t.Fatalf("repeat settlement = %+v, err=%v; want %s without grant", got, err, outcome)
		}
	}
	var afterReplay time.Time
	var grantedEvents int
	if err := pool.QueryRow(context.Background(),
		`SELECT u.plan_expires_at, (SELECT count(*) FROM payment_events WHERE order_id = $2 AND outcome = 'granted')
		 FROM users u WHERE u.id = $1`, f.userID, f.orderID,
	).Scan(&afterReplay, &grantedEvents); err != nil {
		t.Fatal(err)
	}
	if !afterGrant.Equal(afterReplay) || grantedEvents != 1 {
		t.Fatalf("settlement replay expiry/granted events = %v -> %v / %d; want unchanged / 1", afterGrant, afterReplay, grantedEvents)
	}
	requirePromotionCounter(t, pool, promotionID, 1)
}

func TestApplyPromotion_TerminalOrderWinsBeforeLatePatch(t *testing.T) {
	for _, status := range []string{"cancelled", "paid", "expired"} {
		t.Run(status, func(t *testing.T) {
			pool := testOrderDB(t)
			f := newPromotionRaceFixture(t, pool)
			oldCode, oldID := f.code(t, "old", "fixed", 100)
			newCode, newID := f.code(t, "new", "percent", 25)
			requirePromotionHTTP(t, f.request("PATCH", "/promotion", oldCode), fiber.StatusOK)
			before := requirePromotionSnapshot(t, f, "pending", 900, 100, oldID, "reserved")
			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
			// Keep the terminal transition uncommitted until PATCH is waiting
			// on it. This also checks pending eligibility after a lock wait,
			// not merely the easy case of an already-visible terminal order.
			if _, err := tx.Exec(ctx, `UPDATE plan_orders SET status = $2 WHERE id = $1`, f.orderID, status); err != nil {
				t.Fatal(err)
			}
			redemptionStatus, counter := "released", 0
			if status == "paid" {
				redemptionStatus, counter = "confirmed", 1
			}
			if _, err := tx.Exec(ctx, `UPDATE promotion_redemptions SET status = $2 WHERE order_id = $1`, f.orderID, redemptionStatus); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `UPDATE promotion_codes SET redeemed_count = $2 WHERE id = $1`, oldID, counter); err != nil {
				t.Fatal(err)
			}
			patch := startPromotionRequest(t, f, "PATCH", "/promotion", newCode)
			waitPromotionBlockedBy(t, pool, tx.Conn().PgConn().PID(), "plan_orders")
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			requirePromotionHTTP(t, awaitPromotionRequest(t, patch), fiber.StatusNotFound)
			after := requirePromotionSnapshot(t, f, status, 900, 100, oldID, redemptionStatus)
			if after.redemptionID != before.redemptionID {
				t.Fatal("late PATCH replaced the terminal order's ledger row")
			}
			requirePromotionCounter(t, pool, oldID, counter)
			requirePromotionCounter(t, pool, newID, 0)
		})
	}
}

func TestApplyPromotion_ReplacementsUseOriginalPriceAndOneLedgerRow(t *testing.T) {
	pool := testOrderDB(t)
	f := newPromotionRaceFixture(t, pool)
	codeA, idA := f.code(t, "ten-percent", "percent", 10)
	codeB, idB := f.code(t, "quarter-off", "percent", 25)
	var ledgerID string
	for _, step := range []struct {
		code, id        string
		price, discount int64
	}{
		{codeA, idA, 900, 100}, {codeA, idA, 900, 100},
		{codeB, idB, 750, 250}, {codeB, idB, 750, 250}, {codeA, idA, 900, 100},
	} {
		response := f.request("PATCH", "/promotion", step.code)
		requirePromotionHTTP(t, response, fiber.StatusOK)
		var order orderResponse
		if err := json.Unmarshal(response.body, &order); err != nil {
			t.Fatal(err)
		}
		if order.PriceCents != step.price || order.DiscountCents != step.discount {
			t.Fatalf("replacement response price/discount = %d/%d; want %d/%d", order.PriceCents, order.DiscountCents, step.price, step.discount)
		}
		state := requirePromotionSnapshot(t, f, "pending", step.price, step.discount, step.id, "reserved")
		if ledgerID == "" {
			ledgerID = state.redemptionID
		} else if state.redemptionID != ledgerID {
			t.Fatal("replacement must reuse the order's single ledger row")
		}
		for _, id := range []string{idA, idB} {
			want := 0
			if id == step.id {
				want = 1
			}
			requirePromotionCounter(t, pool, id, want)
		}
	}
}

func TestApplyPromotion_InvalidReplacementRollsBackExistingReservation(t *testing.T) {
	pool := testOrderDB(t)
	f := newPromotionRaceFixture(t, pool)
	oldCode, oldID := f.code(t, "valid", "percent", 10)
	invalidCode, invalidID := f.code(t, "inactive", "fixed", 200)
	if _, err := pool.Exec(context.Background(), `UPDATE promotion_codes SET is_active = false WHERE id = $1`, invalidID); err != nil {
		t.Fatal(err)
	}
	requirePromotionHTTP(t, f.request("PATCH", "/promotion", oldCode), fiber.StatusOK)
	before := requirePromotionSnapshot(t, f, "pending", 900, 100, oldID, "reserved")
	for _, code := range []string{invalidCode, f.suffix + "-missing"} {
		got := f.request("PATCH", "/promotion", code)
		requirePromotionHTTP(t, got, fiber.StatusBadRequest)
		if !strings.Contains(string(got.body), "promotion_rejected") {
			t.Fatalf("invalid replacement response = %s; want promotion_rejected", got.body)
		}
		if after := promotionSnapshot(t, f); after != before {
			t.Fatalf("rejected replacement changed order/ledger: before=%+v after=%+v", before, after)
		}
		requirePromotionCounter(t, pool, oldID, 1)
		requirePromotionCounter(t, pool, invalidID, 0)
	}
}

func TestApplyPromotion_ConcurrentReapplicationsAndOppositeSwaps(t *testing.T) {
	pool := testOrderDB(t)
	first := newPromotionRaceFixture(t, pool)
	second := newPromotionRaceFixture(t, pool)
	codeA, idA := first.code(t, "swap-a", "percent", 10)
	codeB, idB := first.code(t, "swap-b", "percent", 25)
	// Two slots allow both orders to transiently use the same code during
	// a swap; per-user cap 1 still detects leaked same-order reservations.
	if _, err := pool.Exec(context.Background(), `UPDATE promotion_codes SET max_redemptions = 2 WHERE id IN ($1, $2)`, idA, idB); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	burst := make(chan promotionHTTPResult, 8)
	for i := 0; i < cap(burst); i++ {
		go func() {
			<-start
			burst <- first.request("PATCH", "/promotion", codeA)
		}()
	}
	close(start)
	for i := 0; i < cap(burst); i++ {
		requirePromotionHTTP(t, awaitPromotionRequest(t, burst), fiber.StatusOK)
	}
	requirePromotionSnapshot(t, first, "pending", 900, 100, idA, "reserved")
	requirePromotionCounter(t, pool, idA, 1)
	requirePromotionHTTP(t, second.request("PATCH", "/promotion", codeB), fiber.StatusOK)

	start = make(chan struct{})
	swaps := make(chan promotionHTTPResult, 2)
	for _, swap := range []struct {
		fixture *promotionRaceFixture
		code    string
	}{{first, codeB}, {second, codeA}} {
		go func(f *promotionRaceFixture, code string) {
			<-start
			swaps <- f.request("PATCH", "/promotion", code)
		}(swap.fixture, swap.code)
	}
	close(start)
	for i := 0; i < cap(swaps); i++ {
		requirePromotionHTTP(t, awaitPromotionRequest(t, swaps), fiber.StatusOK)
	}
	requirePromotionSnapshot(t, first, "pending", 750, 250, idB, "reserved")
	requirePromotionSnapshot(t, second, "pending", 900, 100, idA, "reserved")
	requirePromotionCounter(t, pool, idA, 1)
	requirePromotionCounter(t, pool, idB, 1)
}
