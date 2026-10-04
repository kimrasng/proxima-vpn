package scheduler

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed scheduler tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedResetPlan(t *testing.T, pool *pgxpool.Pool, ctx context.Context, name string) string {
	t.Helper()

	var groupID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, name+"-group",
	).Scan(&groupID); err != nil {
		t.Fatalf("seed node group: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM node_groups WHERE id = $1`, groupID) })

	var planID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		 VALUES ($1, 30, 1, $2) RETURNING id::text`,
		name, groupID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id = $1`, planID) })
	return planID
}

func seedResetUser(t *testing.T, pool *pgxpool.Pool, ctx context.Context, email string, planID *string, startedAt, expiresAt, resetAt *time.Time, trafficUsed int64, status string) string {
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

func readUserState(t *testing.T, pool *pgxpool.Pool, ctx context.Context, userID string) (trafficUsed int64, resetAt *time.Time, status string) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`SELECT traffic_used, traffic_reset_at, status FROM users WHERE id = $1`, userID,
	).Scan(&trafficUsed, &resetAt, &status); err != nil {
		t.Fatalf("read user state: %v", err)
	}
	return
}

// A plan anchored on the 31st has no matching day in a 30-day month under
// day-of-month matching, which is the bug this test locks against: it must
// still reset in February (clamped to the 28th/29th) and again in March.
func TestTrafficReset_Day31AnchorResetsInShortMonths(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewTrafficResetScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("day31-%d", os.Getpid()))
	// Anchor two months ago on the 31st-equivalent: use a fixed past date
	// clearly more than one period behind NOW(), so a reset is due regardless
	// of which day of the current month the test happens to run on.
	start := time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC)
	expires := time.Now().Add(365 * 24 * time.Hour)
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("day31-%d@x.test", os.Getpid()), &planID, &start, &expires, nil, 5000, "active")

	sched.run(ctx)

	trafficUsed, resetAt, _ := readUserState(t, pool, ctx, userID)
	if trafficUsed != 0 {
		t.Errorf("traffic_used = %d, want 0 after reset", trafficUsed)
	}
	if resetAt == nil {
		t.Fatal("traffic_reset_at is nil, want set")
	}
}

// Once a period has been reset, running again immediately must not reset it
// a second time - traffic_reset_at now sits at or after the period start, so
// the WHERE clause's comparison must exclude it.
func TestTrafficReset_DoesNotFireTwiceInSamePeriod(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewTrafficResetScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("noDouble-%d", os.Getpid()))
	start := time.Now().Add(-40 * 24 * time.Hour) // one period elapsed
	expires := time.Now().Add(365 * 24 * time.Hour)
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("noDouble-%d@x.test", os.Getpid()), &planID, &start, &expires, nil, 5000, "active")

	sched.run(ctx)
	_, resetAt1, _ := readUserState(t, pool, ctx, userID)
	if resetAt1 == nil {
		t.Fatal("first run: traffic_reset_at is nil, want set")
	}

	// Bump usage back up and run again immediately - a second fire in the
	// same period would wipe it again, which must not happen.
	if _, err := pool.Exec(ctx, `UPDATE users SET traffic_used = 999 WHERE id = $1`, userID); err != nil {
		t.Fatalf("bump usage: %v", err)
	}
	sched.run(ctx)

	trafficUsed2, resetAt2, _ := readUserState(t, pool, ctx, userID)
	if trafficUsed2 != 999 {
		t.Errorf("traffic_used = %d after second run, want 999 (must not reset twice in one period)", trafficUsed2)
	}
	if !resetAt2.Equal(*resetAt1) {
		t.Errorf("traffic_reset_at changed on second run: %v -> %v, want unchanged", resetAt1, resetAt2)
	}
}

// A term that has already expired must never reset again - that is the "stops
// once the term expires" exit criterion, enforced by NOW() < plan_expires_at
// rather than by relying on ExpiryCheckScheduler's separate status flip.
func TestTrafficReset_DoesNotFirePastExpiry(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewTrafficResetScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("expired-%d", os.Getpid()))
	start := time.Now().Add(-100 * 24 * time.Hour)
	expires := time.Now().Add(-10 * 24 * time.Hour) // already expired
	// status is still 'active': simulates ExpiryCheckScheduler not having
	// ticked yet, which is exactly the race this query must not depend on.
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("expired-%d@x.test", os.Getpid()), &planID, &start, &expires, nil, 5000, "active")

	sched.run(ctx)

	trafficUsed, _, _ := readUserState(t, pool, ctx, userID)
	if trafficUsed != 5000 {
		t.Errorf("traffic_used = %d, want unchanged 5000 (reset must not fire past expiry)", trafficUsed)
	}
}

// The reset is the mechanism meant to lift a traffic-limit suspension; a
// suspended user must be un-suspended by it rather than staying locked out
// until their term ends.
func TestTrafficReset_UnsuspendsOnReset(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewTrafficResetScheduler(pool)

	planID := seedResetPlan(t, pool, ctx, fmt.Sprintf("unsuspend-%d", os.Getpid()))
	start := time.Now().Add(-40 * 24 * time.Hour)
	expires := time.Now().Add(365 * 24 * time.Hour)
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("unsuspend-%d@x.test", os.Getpid()), &planID, &start, &expires, nil, 999999, "suspended")

	sched.run(ctx)

	_, _, status := readUserState(t, pool, ctx, userID)
	if status != "active" {
		t.Errorf("status = %q after reset, want %q", status, "active")
	}
}

// A user without an active plan (never granted one, or is_active=false) must
// not be touched by the query at all.
func TestTrafficReset_SkipsUsersWithoutAnActivePlan(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	sched := NewTrafficResetScheduler(pool)

	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("noplan-%d@x.test", os.Getpid()), nil, nil, nil, nil, 42, "pending")

	sched.run(ctx)

	trafficUsed, _, _ := readUserState(t, pool, ctx, userID)
	if trafficUsed != 42 {
		t.Errorf("traffic_used = %d, want unchanged 42 (no plan means nothing to reset)", trafficUsed)
	}
}
