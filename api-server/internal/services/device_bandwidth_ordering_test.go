package services_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/redis/go-redis/v9"
)

// Delay a real authorization result while its transaction remains open. This
// models an old high-rate request reaching Redis later than a plan edit.
type delayedBandwidthDB struct {
	pool    *pgxpool.Pool
	read    chan struct{}
	release chan struct{}
}

func (db delayedBandwidthDB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return delayedBandwidthTx{Tx: tx, db: db}, nil
}

type delayedBandwidthTx struct {
	pgx.Tx
	db delayedBandwidthDB
}

func (tx delayedBandwidthTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if !strings.HasPrefix(sql, "SELECT p.speed_limit") {
		return tx.Tx.QueryRow(ctx, sql, args...)
	}
	return delayedBandwidthRow{Row: tx.Tx.QueryRow(ctx, sql, args...), ctx: ctx, db: tx.db}
}

type delayedBandwidthRow struct {
	pgx.Row
	ctx context.Context
	db  delayedBandwidthDB
}

func (row delayedBandwidthRow) Scan(dest ...any) error {
	if err := row.Row.Scan(dest...); err != nil {
		return err
	}
	close(row.db.read)
	select {
	case <-row.db.release:
		return nil
	case <-row.ctx.Done():
		return row.ctx.Err()
	}
}

func TestDeviceBandwidthPermitPlanDowngradeCannotBeOverwrittenByDelayedAuthorization(t *testing.T) {
	testBandwidthDelayedDowngrade(t, false)
}

func TestDeviceBandwidthPermitPlanTransferCannotBeOverwrittenByDelayedAuthorization(t *testing.T) {
	testBandwidthDelayedDowngrade(t, true)
}

func testBandwidthDelayedDowngrade(t *testing.T, transfer bool) {
	t.Helper()
	pool := testDB(t)
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set; skipping bandwidth downgrade ordering test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	suffix := crypto.NewUUID()
	planID := seedPlan(t, pool, ctx, "bandwidth-order-"+suffix, 30, nil)
	if _, err := pool.Exec(ctx, `UPDATE plans SET speed_limit=100 WHERE id=$1`, planID); err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool, ctx, "bandwidth-order-"+suffix+"@example.test", planID, nil, nil, nil, 0, "active")
	deviceUUID := crypto.NewUUID()
	if _, err := pool.Exec(ctx, `INSERT INTO devices (user_id, xray_uuid) VALUES ($1,$2)`, userID, deviceUUID); err != nil {
		t.Fatal(err)
	}
	key := "device_bandwidth:" + deviceUUID + ":upload"
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })
	var nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO nodes (name, api_key, ip, role, status) VALUES ($1,'unused','203.0.113.80','exit','offline') RETURNING id::text`, "bandwidth-order-"+suffix).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=$1`, nodeID) })
	if _, err := pool.Exec(ctx, `INSERT INTO node_group_nodes (node_group_id,node_id) SELECT node_group_id,$2 FROM plans WHERE id=$1`, planID, nodeID); err != nil {
		t.Fatal(err)
	}

	updateSQL := `UPDATE plans SET speed_limit=1 WHERE id=$1`
	updateArgs := []any{planID}
	if transfer {
		lowPlan := seedPlan(t, pool, ctx, "bandwidth-low-"+suffix, 30, nil)
		// Both plans use the same Exit group so only the entitlement rate changes.
		if _, err := pool.Exec(ctx, `UPDATE plans SET speed_limit=1, node_group_id=(SELECT node_group_id FROM plans WHERE id=$2) WHERE id=$1`, lowPlan, planID); err != nil {
			t.Fatal(err)
		}
		updateSQL = `UPDATE users SET plan_id=$2 WHERE id=$1`
		updateArgs = []any{userID, lowPlan}
		// Remove the user before the lower plan in LIFO fixture cleanup.
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })
	}

	db := delayedBandwidthDB{pool: pool, read: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(db.release) }) }
	defer release()
	type permitResult struct {
		permit services.DeviceBandwidthPermit
		err    error
	}
	oldDone := make(chan permitResult, 1)
	go func() {
		permit, err := services.NewDeviceBandwidthService(db, rdb).Permit(ctx, nodeID, deviceUUID, "upload", 1)
		oldDone <- permitResult{permit, err}
	}()
	select {
	case <-db.read:
	case <-ctx.Done():
		t.Fatal("old authorization did not complete")
	}

	updateTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		release()
		cancel()
		_ = updateTx.Rollback(context.Background())
	}()
	var updatePID int
	if err := updateTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&updatePID); err != nil {
		t.Fatal(err)
	}
	updateDone := make(chan error, 1)
	go func() {
		_, err := updateTx.Exec(ctx, updateSQL, updateArgs...)
		if err == nil {
			err = updateTx.Commit(ctx)
		}
		updateDone <- err
	}()
	// Observe a real database lock wait, not just a timing assumption. Without
	// the service's row locks, the edit commits while the old result is delayed.
	for {
		select {
		case err := <-updateDone:
			t.Fatalf("downgrade finished before old permit consumed: %v", err)
		default:
		}
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1)) > 0`, updatePID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("downgrade did not wait for the authorization lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	release()
	old := <-oldDone
	if old.err != nil || !old.permit.Allowed {
		t.Fatalf("old permit = %+v, %v", old.permit, old.err)
	}
	if err := <-updateDone; err != nil {
		t.Fatalf("downgrade commit: %v", err)
	}
	current, err := services.NewDeviceBandwidthService(pool, rdb).Permit(ctx, nodeID, deviceUUID, "upload", 65535)
	if err != nil || current.Allowed || current.RetryAfterMS != 525 {
		t.Fatalf("post-downgrade permit = %+v, %v; want denial and 525ms, not stale high-rate credit", current, err)
	}
	if rate, err := rdb.HGet(ctx, key, "rate").Result(); err != nil || rate != "125000" {
		t.Fatalf("final central rate = %q, %v; want 125000", rate, err)
	}
	if !transfer {
		return
	}

	// Also exercise a transfer that holds the user row before a permit begins.
	// The permit must wait, then select the newly assigned plan rather than a
	// high-plan join from the statement's pre-wait snapshot.
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_id=$2 WHERE id=$1`, userID, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := services.NewDeviceBandwidthService(pool, rdb).Permit(ctx, nodeID, deviceUUID, "upload", 1); err != nil {
		t.Fatal(err)
	}
	transferTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer transferTx.Rollback(context.Background())
	if _, err := transferTx.Exec(ctx, updateSQL, updateArgs...); err != nil {
		t.Fatal(err)
	}
	permitConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer permitConn.Release()
	var permitPID int
	if err := permitConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&permitPID); err != nil {
		t.Fatal(err)
	}
	waitingDone := make(chan permitResult, 1)
	go func() {
		permit, err := services.NewDeviceBandwidthService(permitConn, rdb).Permit(ctx, nodeID, deviceUUID, "upload", 65535)
		waitingDone <- permitResult{permit, err}
	}()
	// Always unblock and join the goroutine before releasing its connection.
	defer func() {
		cancel()
		_ = transferTx.Rollback(context.Background())
		if waitingDone != nil {
			<-waitingDone
		}
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1)) > 0`, permitPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case result := <-waitingDone:
			waitingDone = nil
			t.Fatalf("permit did not wait for pending assignment transfer: %+v, %v", result.permit, result.err)
		case <-ctx.Done():
			t.Fatal("permit did not reach the assignment lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := transferTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-waitingDone
	waitingDone = nil
	if result.err != nil || result.permit.Allowed || result.permit.RetryAfterMS != 525 {
		t.Fatalf("permit after waiting for transfer = %+v, %v; want current low-plan denial", result.permit, result.err)
	}
	if rate, err := rdb.HGet(ctx, key, "rate").Result(); err != nil || rate != "125000" {
		t.Fatalf("rate after waiting for transfer = %q, %v; want 125000", rate, err)
	}
}
