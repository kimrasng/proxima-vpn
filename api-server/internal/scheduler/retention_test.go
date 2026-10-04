package scheduler

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedPurgeOrder inserts a plan_orders row with checkout audit fields and financial data,
// cleaning it up on test completion.
func seedPurgeOrder(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID, planID string, createdAt time.Time, trackingFilled bool) string {
	t.Helper()

	// Tracking fields: only populated if trackingFilled=true (simulates orders that carried checkout audit)
	var origin, clientIP, userAgent, browserFamily, osFamily, locale, deviceFP *string
	if trackingFilled {
		o := "web"
		c := "192.0.2.1"
		u := "Mozilla/5.0"
		b := "Chrome"
		s := "Linux"
		l := "en-US"
		d := "fp-abc123"
		origin = &o
		clientIP = &c
		userAgent = &u
		browserFamily = &b
		osFamily = &s
		locale = &l
		deviceFP = &d
	}

	// Financial fields: always populated to prove they survive purge
	paymentProvider := "stripe"
	paymentSessionID := "cs_test_123"
	grantBefore := createdAt.Add(-time.Hour)
	grantAfter := createdAt.Add(30 * 24 * time.Hour)

	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plan_orders (
			user_id, plan_id, duration_days, price_cents, status,
			created_at, paid_at, paid_by, provider, provider_session_id,
			granted_plan_expires_before, granted_plan_expires_after,
			origin, client_ip, user_agent, browser_family, os_family, locale, device_fingerprint
		) VALUES (
			$1, $2, 30, 1000, 'paid',
			$3, $4, 'manual', $5, $6,
			$7, $8,
			$9, $10, $11, $12, $13, $14, $15
		) RETURNING id::text`,
		userID, planID, createdAt, createdAt, paymentProvider, paymentSessionID,
		grantBefore, grantAfter,
		origin, clientIP, userAgent, browserFamily, osFamily, locale, deviceFP,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed purge order: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM plan_orders WHERE id = $1`, orderID) })
	return orderID
}

// readPurgeOrder reads tracking and financial fields from a plan_orders row.
func readPurgeOrder(t *testing.T, pool *pgxpool.Pool, ctx context.Context, orderID string) (
	origin, clientIP, userAgent *string,
	paymentProvider string,
	grantBefore, grantAfter *time.Time,
) {
	t.Helper()
	if err := pool.QueryRow(ctx, `
		SELECT origin, client_ip, user_agent, provider, granted_plan_expires_before, granted_plan_expires_after
		FROM plan_orders WHERE id = $1
	`, orderID).Scan(&origin, &clientIP, &userAgent, &paymentProvider, &grantBefore, &grantAfter); err != nil {
		t.Fatalf("read purge order: %v", err)
	}
	return
}

// TestRetention_PurgesOldPlanOrdersTracking verifies that plan_orders rows older than
// 180 days have their checkout audit tracking fields NULLed while preserving financial evidence.
func TestRetention_PurgesOldPlanOrdersTracking(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewRetentionScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("purge-old-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("purge-old-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "active")

	// Old order: 181 days in the past, tracking should be purged
	oldTime := time.Now().Add(-181 * 24 * time.Hour)
	oldOrderID := seedPurgeOrder(t, pool, ctx, userID, planID, oldTime, true)

	sched.run(ctx)

	origin, clientIP, userAgent, provider, grantBefore, grantAfter := readPurgeOrder(t, pool, ctx, oldOrderID)

	if origin != nil {
		t.Errorf("origin = %v, want nil after purge", *origin)
	}
	if clientIP != nil {
		t.Errorf("client_ip = %v, want nil after purge", *clientIP)
	}
	if userAgent != nil {
		t.Errorf("user_agent = %v, want nil after purge", *userAgent)
	}
	// Financial fields must survive
	if provider != "stripe" {
		t.Errorf("provider = %q, want stripe (financial data preserved)", provider)
	}
	if grantBefore == nil {
		t.Error("granted_plan_expires_before is nil, want set (financial data preserved)")
	}
	if grantAfter == nil {
		t.Error("granted_plan_expires_after is nil, want set (financial data preserved)")
	}
}

// TestRetention_PreservesRecentPlanOrders verifies that plan_orders rows newer than
// 180 days keep their checkout audit fields intact.
func TestRetention_PreservesRecentPlanOrders(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewRetentionScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("purge-recent-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("purge-recent-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "active")

	// Recent order: 30 days in the past, tracking should be preserved
	recentTime := time.Now().Add(-30 * 24 * time.Hour)
	recentOrderID := seedPurgeOrder(t, pool, ctx, userID, planID, recentTime, true)

	sched.run(ctx)

	origin, clientIP, userAgent, _, _, _ := readPurgeOrder(t, pool, ctx, recentOrderID)

	if origin == nil || *origin != "web" {
		t.Error("origin was purged, want preserved for recent order")
	}
	if clientIP == nil || *clientIP != "192.0.2.1" {
		t.Error("client_ip was purged, want preserved for recent order")
	}
	if userAgent == nil || *userAgent != "Mozilla/5.0" {
		t.Error("user_agent was purged, want preserved for recent order")
	}
}

// TestRetention_PreservesFinancialData verifies that all financial fields survive
// a purge cycle unmodified.
func TestRetention_PreservesFinancialData(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewRetentionScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("purge-fin-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("purge-fin-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "active")

	// Old order: 181 days in the past, guarantees purge will run
	oldTime := time.Now().Add(-181 * 24 * time.Hour)
	oldOrderID := seedPurgeOrder(t, pool, ctx, userID, planID, oldTime, true)

	sched.run(ctx)

	_, _, _, provider, grantBefore, grantAfter := readPurgeOrder(t, pool, ctx, oldOrderID)

	// Read full order to verify all financial fields
	var status, paidBy string
	var paidAt *time.Time
	var priceCents int64
	if err := pool.QueryRow(ctx, `
		SELECT status, price_cents, paid_at, paid_by FROM plan_orders WHERE id = $1
	`, oldOrderID).Scan(&status, &priceCents, &paidAt, &paidBy); err != nil {
		t.Fatalf("read order financial data: %v", err)
	}

	if status != "paid" {
		t.Errorf("status = %q, want paid (financial status preserved)", status)
	}
	if priceCents != 1000 {
		t.Errorf("price_cents = %d, want 1000 (financial pricing preserved)", priceCents)
	}
	if paidAt == nil {
		t.Error("paid_at is nil, want set (financial payment timestamp preserved)")
	}
	if paidBy != "manual" {
		t.Errorf("paid_by = %q, want manual (financial payment method preserved)", paidBy)
	}
	if provider != "stripe" {
		t.Errorf("provider = %q, want stripe (financial provider preserved)", provider)
	}
	if grantBefore == nil || grantAfter == nil {
		t.Error("grant timestamps are nil, want set (financial grant evidence preserved)")
	}
}

// TestRetention_SecondSweepTerminates verifies that a second purge sweep terminates
// immediately without re-processing rows that were already purged (idempotency).
func TestRetention_SecondSweepTerminates(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewRetentionScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("purge-idem-%d", os.Getpid()))
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("purge-idem-%d@x.test", os.Getpid()), &planID, nil, nil, nil, 0, "active")

	// Old order: 181 days in the past
	oldTime := time.Now().Add(-181 * 24 * time.Hour)
	oldOrderID := seedPurgeOrder(t, pool, ctx, userID, planID, oldTime, true)

	// First sweep: purges tracking fields
	sched.run(ctx)
	origin1, _, _, _, _, _ := readPurgeOrder(t, pool, ctx, oldOrderID)
	if origin1 != nil {
		t.Fatal("first sweep did not purge tracking")
	}

	// Second sweep: should find no rows with user_agent IS NOT NULL (index predicate),
	// so purgeColumns should complete immediately without updates
	sched.run(ctx)

	// Verify order is still purged (no re-population)
	origin2, clientIP2, userAgent2, _, _, _ := readPurgeOrder(t, pool, ctx, oldOrderID)
	if origin2 != nil {
		t.Error("origin repopulated after second sweep, want stable NULL")
	}
	if clientIP2 != nil {
		t.Error("client_ip repopulated after second sweep, want stable NULL")
	}
	if userAgent2 != nil {
		t.Error("user_agent repopulated after second sweep, want stable NULL")
	}
}
