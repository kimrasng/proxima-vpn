package handlers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Plan creation is the path that provisions every user's entitlements, and it
// had no test - so adding max_concurrent to the column list while leaving the
// placeholder list at six shipped a 500 that only surfaced in e2e. These
// exercise the statement against a real database.
func TestPlanCreationInsertsEveryColumnItNames(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	var groupID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`,
		fmt.Sprintf("plan-insert-group-%d", os.Getpid()),
	).Scan(&groupID); err != nil {
		t.Fatalf("seed node group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, groupID)
	})

	var (
		planID        string
		maxDevices    int
		maxConcurrent *int
	)
	err = pool.QueryRow(ctx,
		`INSERT INTO plans (name, traffic_limit, duration_days, max_devices, max_concurrent, speed_limit, node_group_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id::text, max_devices, max_concurrent`,
		fmt.Sprintf("plan-insert-%d", os.Getpid()), nil, 30, 5, 3, nil, groupID,
	).Scan(&planID, &maxDevices, &maxConcurrent)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, planID)
	})

	if maxDevices != 5 {
		t.Errorf("max_devices = %d, want 5", maxDevices)
	}
	if maxConcurrent == nil || *maxConcurrent != 3 {
		t.Errorf("max_concurrent = %v, want 3", maxConcurrent)
	}
}

// A plan created without a concurrency cap must read back as NULL, because
// that is what makes the cap fall back to max_devices rather than to zero -
// zero would deny every connection on the plan.
func TestPlanWithoutConcurrencyCapStoresNull(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	var groupID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`,
		fmt.Sprintf("plan-null-group-%d", os.Getpid()),
	).Scan(&groupID); err != nil {
		t.Fatalf("seed node group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, groupID)
	})

	var (
		planID     string
		effective  int
		storedNull bool
	)
	err = pool.QueryRow(ctx,
		`INSERT INTO plans (name, traffic_limit, duration_days, max_devices, max_concurrent, speed_limit, node_group_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id::text, COALESCE(max_concurrent, max_devices), max_concurrent IS NULL`,
		fmt.Sprintf("plan-null-%d", os.Getpid()), nil, 30, 4, nil, nil, groupID,
	).Scan(&planID, &effective, &storedNull)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, planID)
	})

	if !storedNull {
		t.Error("max_concurrent should be NULL when the request omits it")
	}
	if effective != 4 {
		t.Errorf("effective cap = %d, want 4 (inherited from max_devices)", effective)
	}
}
