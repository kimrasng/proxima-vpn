package services_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// testDB connects to TEST_DATABASE_URL, skipping when it is not set - the
// same convention handlers/*_test.go uses for its Postgres-backed tests.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed service tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedPlan inserts a node_groups row and a plans row with the given
// duration/traffic_limit, cleaning both up on test completion.
func seedPlan(t *testing.T, pool *pgxpool.Pool, ctx context.Context, name string, durationDays int, trafficLimit *int64) string {
	t.Helper()

	var groupID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`,
		name+"-group",
	).Scan(&groupID); err != nil {
		t.Fatalf("seed node group: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM node_groups WHERE id = $1`, groupID) })

	var planID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, traffic_limit, duration_days, max_devices, node_group_id)
		 VALUES ($1, $2, $3, 1, $4) RETURNING id::text`,
		name, trafficLimit, durationDays, groupID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, planID) })
	return planID
}

// seedUser inserts a users row with the given plan state, cleaning it up on
// test completion. Any *time.Time left nil is inserted as SQL NULL.
func seedUser(t *testing.T, pool *pgxpool.Pool, ctx context.Context, email, planID string, startedAt, expiresAt, resetAt *time.Time, trafficUsed int64, status string) string {
	t.Helper()

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name, sub_token, plan_id, plan_started_at, plan_expires_at,
		                     traffic_used, traffic_reset_at, status, is_active)
		 VALUES ($1, 'x', 'x', $1, $2, $3, $4, $5, $6, $7, true)
		 RETURNING id::text`,
		email, planID, startedAt, expiresAt, trafficUsed, resetAt, status,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })
	return userID
}

func TestGrantPlan_RenewalPreservesUsageAndAnchor(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("renew-%d", os.Getpid()), 30, nil)
	start := time.Now().Add(-10 * 24 * time.Hour)
	expires := time.Now().Add(5 * 24 * time.Hour)
	reset := time.Now().Add(-3 * 24 * time.Hour)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("renew-%d@x.test", os.Getpid()), planID, &start, &expires, &reset, 12345, "active")

	result, err := svc.GrantPlan(ctx, userID, planID)
	if err != nil {
		t.Fatalf("GrantPlan: %v", err)
	}
	if result.Kind != services.GrantRenewal {
		t.Errorf("Kind = %q, want %q", result.Kind, services.GrantRenewal)
	}
	if result.TrafficUsed != 12345 {
		t.Errorf("TrafficUsed = %d, want 12345 (renewal must not reset usage)", result.TrafficUsed)
	}
	if diff := result.PlanStartedAt.Sub(start); diff < -time.Second || diff > time.Second {
		t.Errorf("PlanStartedAt = %v, want unchanged %v (renewal must not move the reset anchor)", result.PlanStartedAt, start)
	}
	if result.TrafficResetAt == nil {
		t.Fatal("TrafficResetAt is nil, want set")
	}
	if diff := result.TrafficResetAt.Sub(reset); diff < -time.Second || diff > time.Second {
		t.Errorf("TrafficResetAt = %v, want unchanged %v", result.TrafficResetAt, reset)
	}
	wantExpiry := expires.Add(30 * 24 * time.Hour)
	if diff := result.PlanExpiresAt.Sub(wantExpiry); diff < -time.Second || diff > time.Second {
		t.Errorf("PlanExpiresAt = %v, want ~%v (old expiry + duration)", result.PlanExpiresAt, wantExpiry)
	}
}

func TestGrantPlan_ChangeCarriesRemainderAndResetsUsage(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	oldPlanID := seedPlan(t, pool, ctx, fmt.Sprintf("change-old-%d", os.Getpid()), 30, nil)
	newPlanID := seedPlan(t, pool, ctx, fmt.Sprintf("change-new-%d", os.Getpid()), 60, nil)
	start := time.Now().Add(-10 * 24 * time.Hour)
	expires := time.Now().Add(5 * 24 * time.Hour) // 5 days of unexpired remainder
	userID := seedUser(t, pool, ctx, fmt.Sprintf("change-%d@x.test", os.Getpid()), oldPlanID, &start, &expires, nil, 99999, "active")

	before := time.Now()
	result, err := svc.GrantPlan(ctx, userID, newPlanID)
	if err != nil {
		t.Fatalf("GrantPlan: %v", err)
	}
	if result.Kind != services.GrantChange {
		t.Errorf("Kind = %q, want %q", result.Kind, services.GrantChange)
	}
	if result.TrafficUsed != 0 {
		t.Errorf("TrafficUsed = %d, want 0 (a plan change clears usage)", result.TrafficUsed)
	}
	if result.PlanStartedAt.Before(before) {
		t.Errorf("PlanStartedAt = %v, want re-anchored to now (>= %v)", result.PlanStartedAt, before)
	}
	// Carried remainder ~5 days, new duration 60 days: expect ~65 days from now.
	wantExpiry := before.Add(65 * 24 * time.Hour)
	if diff := result.PlanExpiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("PlanExpiresAt = %v, want ~%v (carried remainder + new duration)", result.PlanExpiresAt, wantExpiry)
	}
	if result.Carried <= 0 {
		t.Errorf("Carried = %v, want > 0 (grant was before expiry)", result.Carried)
	}
}

func TestGrantPlan_ExpiredRepurchaseStartsFreshTermNoCarry(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("fresh-%d", os.Getpid()), 30, nil)
	start := time.Now().Add(-40 * 24 * time.Hour)
	expires := time.Now().Add(-10 * 24 * time.Hour) // already expired
	userID := seedUser(t, pool, ctx, fmt.Sprintf("fresh-%d@x.test", os.Getpid()), planID, &start, &expires, nil, 500, "expired")

	before := time.Now()
	result, err := svc.GrantPlan(ctx, userID, planID)
	if err != nil {
		t.Fatalf("GrantPlan: %v", err)
	}
	if result.Kind != services.GrantFresh {
		t.Errorf("Kind = %q, want %q", result.Kind, services.GrantFresh)
	}
	if result.Carried != 0 {
		t.Errorf("Carried = %v, want 0 (nothing to carry past expiry)", result.Carried)
	}
	if result.TrafficUsed != 0 {
		t.Errorf("TrafficUsed = %d, want 0", result.TrafficUsed)
	}
	wantExpiry := before.Add(30 * 24 * time.Hour)
	if diff := result.PlanExpiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("PlanExpiresAt = %v, want ~%v (NOW + duration, no carry)", result.PlanExpiresAt, wantExpiry)
	}
	if result.Status != "active" {
		t.Errorf("Status = %q, want %q", result.Status, "active")
	}
}

func TestGrantPlan_RenewalOfCappedOverLimitYieldsSuspended(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	limit := int64(1000)
	planID := seedPlan(t, pool, ctx, fmt.Sprintf("capped-%d", os.Getpid()), 30, &limit)
	start := time.Now().Add(-10 * 24 * time.Hour)
	expires := time.Now().Add(5 * 24 * time.Hour)
	// Over the cap and already suspended by ExpiryCheckScheduler.
	userID := seedUser(t, pool, ctx, fmt.Sprintf("capped-%d@x.test", os.Getpid()), planID, &start, &expires, nil, 2000, "suspended")

	result, err := svc.GrantPlan(ctx, userID, planID)
	if err != nil {
		t.Fatalf("GrantPlan: %v", err)
	}
	if result.Kind != services.GrantRenewal {
		t.Errorf("Kind = %q, want %q", result.Kind, services.GrantRenewal)
	}
	if result.Status != "suspended" {
		t.Errorf("Status = %q, want %q (renewal preserves usage, so the cap violation is still true)", result.Status, "suspended")
	}
}

func TestGrantPlan_UnknownPlanErrors(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("known-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("known-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")

	if _, err := svc.GrantPlan(ctx, userID, "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Error("GrantPlan with a nonexistent plan id: want error, got nil")
	}
}

func TestGrantPlan_UnknownUserErrors(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("nouser-%d", os.Getpid()), 30, nil)

	if _, err := svc.GrantPlan(ctx, "00000000-0000-0000-0000-000000000000", planID); err == nil {
		t.Error("GrantPlan with a nonexistent user id: want error, got nil")
	}
}

// GrantPlanWithDuration is what the order-paid path calls: the plan's
// duration_days (30 here) must not be what the term is computed from - the
// order's chosen duration (365, simulating a plan sold at several durations)
// is. Everything else about the grant (fresh term here, since the user has no
// prior plan) stays identical to GrantPlan's own behavior.
func TestGrantPlanWithDuration_OverridesPlanDurationDays(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("orderdur-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("orderdur-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")

	before := time.Now()
	result, err := svc.GrantPlanWithDuration(ctx, userID, planID, 365)
	if err != nil {
		t.Fatalf("GrantPlanWithDuration: %v", err)
	}
	if result.Kind != services.GrantFresh {
		t.Errorf("Kind = %q, want %q", result.Kind, services.GrantFresh)
	}
	wantExpiry := before.Add(365 * 24 * time.Hour)
	if diff := result.PlanExpiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("PlanExpiresAt = %v, want ~%v (order's 365 days, not the plan's 30)", result.PlanExpiresAt, wantExpiry)
	}
}

// A renewal that specifies an explicit duration still extends from the prior
// expiry, and still preserves usage/anchor - GrantPlanWithDuration only
// changes which duration feeds the formula, not the renew/change/fresh
// classification GrantPlan already has tests for above.
func TestGrantPlanWithDuration_RenewalStillExtendsFromOldExpiry(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("orderrenew-%d", os.Getpid()), 30, nil)
	start := time.Now().Add(-10 * 24 * time.Hour)
	expires := time.Now().Add(5 * 24 * time.Hour)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("orderrenew-%d@x.test", os.Getpid()), planID, &start, &expires, nil, 777, "active")

	result, err := svc.GrantPlanWithDuration(ctx, userID, planID, 90)
	if err != nil {
		t.Fatalf("GrantPlanWithDuration: %v", err)
	}
	if result.Kind != services.GrantRenewal {
		t.Errorf("Kind = %q, want %q", result.Kind, services.GrantRenewal)
	}
	if result.TrafficUsed != 777 {
		t.Errorf("TrafficUsed = %d, want unchanged 777", result.TrafficUsed)
	}
	wantExpiry := expires.Add(90 * 24 * time.Hour)
	if diff := result.PlanExpiresAt.Sub(wantExpiry); diff < -time.Second || diff > time.Second {
		t.Errorf("PlanExpiresAt = %v, want ~%v (old expiry + ordered 90 days)", result.PlanExpiresAt, wantExpiry)
	}
}
