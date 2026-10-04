package services

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/redis/go-redis/v9"
)

const bandwidthTestNode = "c718ef90-7d20-4c94-b43b-58c7dce5382e"
const bandwidthTestDevice = "3a2ecdb6-6283-46c0-a971-e9adeea2a27e"

type bandwidthAuthorizationDB struct {
	speed     *int64
	err       error
	calls     int
	mu        sync.Mutex
	beginErr  error
	commitErr error
}

func (db *bandwidthAuthorizationDB) Begin(context.Context) (pgx.Tx, error) {
	if db.beginErr != nil {
		return nil, db.beginErr
	}
	return &bandwidthAuthorizationTx{db: db}, nil
}

type bandwidthAuthorizationTx struct {
	pgx.Tx
	db *bandwidthAuthorizationDB
}

func (tx *bandwidthAuthorizationTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.db.QueryRow(ctx, sql, args...)
}

func (tx *bandwidthAuthorizationTx) Commit(context.Context) error   { return tx.db.commitErr }
func (tx *bandwidthAuthorizationTx) Rollback(context.Context) error { return nil }

func (db *bandwidthAuthorizationDB) QueryRow(context.Context, string, ...any) pgx.Row {
	db.mu.Lock()
	db.calls++
	db.mu.Unlock()
	return bandwidthAuthorizationRow{db}
}

type bandwidthAuthorizationRow struct{ db *bandwidthAuthorizationDB }

func (row bandwidthAuthorizationRow) Scan(dest ...any) error {
	if row.db.err != nil {
		return row.db.err
	}
	switch target := dest[0].(type) {
	case **int64:
		*target = row.db.speed
		if len(dest) > 1 {
			*dest[1].(*int) = 100000
		}
	case *string:
		*target = bandwidthTestNode
	case **string:
		planID := bandwidthTestNode
		*target = &planID
	default:
		return errors.New("unexpected authorization result type")
	}
	return nil
}

func TestDeviceBandwidthPermitRequiresAuthorizationEvenWhenUnlimited(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		allowed bool
		wantErr bool
	}{
		{name: "authorized unlimited device", wantErr: true}, // Redis is required even for unlimited plans.
		{name: "unknown or ineligible device", err: pgx.ErrNoRows},
		{name: "database unavailable", err: errors.New("database unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &bandwidthAuthorizationDB{err: tc.err}
			permit, err := NewDeviceBandwidthService(db, nil).Permit(context.Background(), bandwidthTestNode, bandwidthTestDevice, "upload", 65535)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Permit error = %v, want error %v", err, tc.wantErr)
			}
			if permit.Allowed != tc.allowed || permit.RetryAfterMS != 0 {
				t.Errorf("Permit = %+v, want allowed=%v, retry_after_ms=0", permit, tc.allowed)
			}
			wantCalls := 4 // Device lock, assignment lock, plan lock, fresh eligibility.
			if tc.err != nil {
				wantCalls = 1
			}
			if db.calls != wantCalls {
				t.Errorf("authorization calls = %d, want %d", db.calls, wantCalls)
			}
		})
	}
}

func TestDeviceBandwidthPermitTransactionFailuresFailClosed(t *testing.T) {
	for _, db := range []*bandwidthAuthorizationDB{
		{beginErr: errors.New("begin failed")},
		{commitErr: errors.New("commit failed")},
	} {
		permit, err := NewDeviceBandwidthService(db, nil).Permit(context.Background(), bandwidthTestNode, bandwidthTestDevice, "upload", 1)
		if err == nil || permit.Allowed {
			t.Fatalf("transaction failure = %+v, %v; want fail-closed error", permit, err)
		}
	}
}

func TestDeviceBandwidthPermitRejectsInvalidRequestsBeforeAuthorization(t *testing.T) {
	for _, tc := range []struct {
		node, device, direction string
		bytes                   int
	}{
		{"invalid", bandwidthTestDevice, "upload", 1},
		{bandwidthTestNode, "invalid", "upload", 1},
		{bandwidthTestNode, bandwidthTestDevice, "both", 1},
		{bandwidthTestNode, bandwidthTestDevice, "upload", 0},
		{bandwidthTestNode, bandwidthTestDevice, "download", -1},
		{bandwidthTestNode, bandwidthTestDevice, "upload", 65536},
	} {
		db := &bandwidthAuthorizationDB{}
		permit, err := NewDeviceBandwidthService(db, nil).Permit(context.Background(), tc.node, tc.device, tc.direction, tc.bytes)
		if !errors.Is(err, ErrInvalidBandwidthRequest) || permit.Allowed || db.calls != 0 {
			t.Errorf("invalid request %+v: permit=%+v, error=%v, authorization calls=%d", tc, permit, err, db.calls)
		}
	}
}

func TestDeviceBandwidthPermitLimitedPlansFailClosedWithoutRedis(t *testing.T) {
	mbps := int64(10)
	permit, err := NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, nil).Permit(context.Background(), bandwidthTestNode, bandwidthTestDevice, "upload", 1)
	if err == nil || permit.Allowed {
		t.Fatalf("Permit = %+v, %v; want fail-closed error", permit, err)
	}
}

func newBandwidthRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set; skipping Redis-backed device bandwidth tests")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("connect test Redis: %v", err)
	}
	return rdb
}

func bandwidthDevice(t *testing.T, rdb *redis.Client) string {
	t.Helper()
	device := crypto.NewUUID()
	t.Cleanup(func() {
		if err := rdb.Del(context.Background(), "device_bandwidth:"+device+":upload", "device_bandwidth:"+device+":download").Err(); err != nil {
			t.Errorf("clean up bandwidth buckets: %v", err)
		}
	})
	return device
}

func TestDeviceBandwidthPermitSharesBudgetAcrossExitsAndSeparatesDevicesAndDirections(t *testing.T) {
	rdb := newBandwidthRedis(t)
	device := bandwidthDevice(t, rdb)
	otherDevice := bandwidthDevice(t, rdb)
	mbps := int64(1) // 125000 bytes/s, 65535-byte minimum burst.
	svc := NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, rdb)
	otherExit := NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, rdb)
	ctx := context.Background()
	first, err := svc.Permit(ctx, bandwidthTestNode, device, "upload", 65535)
	if err != nil || !first.Allowed || first.RetryAfterMS != 0 {
		t.Fatalf("initial burst = %+v, %v", first, err)
	}
	for i := 0; i < 3; i++ {
		denied, err := otherExit.Permit(ctx, "12465e85-b31c-4967-8a1b-8b121268d4cf", strings.ToUpper(device), "upload", 65535)
		if err != nil || denied.Allowed || denied.RetryAfterMS < 1 || denied.RetryAfterMS > 525 {
			t.Fatalf("same-device denial at another Exit = %+v, %v", denied, err)
		}
	}
	for _, tc := range []struct{ device, direction string }{{device, "download"}, {otherDevice, "upload"}} {
		permit, err := svc.Permit(ctx, bandwidthTestNode, tc.device, tc.direction, 65535)
		if err != nil || !permit.Allowed {
			t.Fatalf("independent bucket %+v = %+v, %v", tc, permit, err)
		}
	}
	time.Sleep(550 * time.Millisecond)
	refilled, err := otherExit.Permit(ctx, bandwidthTestNode, device, "upload", 65535)
	if err != nil || !refilled.Allowed {
		t.Fatalf("retry after full refill = %+v, %v; denials must not reserve tokens", refilled, err)
	}
}

func TestDeviceBandwidthPermitUsesPlanRateAndBoundedBurst(t *testing.T) {
	rdb := newBandwidthRedis(t)
	for _, tc := range []struct {
		mbps    int64
		allowed int
	}{
		{1, 1},  // min burst = 65535.
		{10, 1}, // 125000-byte burst cannot grant two 65535-byte requests.
		{21, 4}, // max burst = 262144, capped below the 262500-byte tenth-second rate.
	} {
		device := bandwidthDevice(t, rdb)
		svc := NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &tc.mbps}, rdb)
		// Set the saved timestamp to Redis's current time before each request,
		// minimizing elapsed refill during the initial burst checks.
		for i := 0; i <= tc.allowed; i++ {
			if i > 0 {
				if err := rdb.Eval(context.Background(), `local clock=redis.call('TIME'); return redis.call('HSET',KEYS[1],'timestamp',tonumber(clock[1])*1000+tonumber(clock[2])/1000)`, []string{"device_bandwidth:" + device + ":upload"}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			permit, err := svc.Permit(context.Background(), bandwidthTestNode, device, "upload", 65535)
			if err != nil || permit.Allowed != (i < tc.allowed) {
				t.Fatalf("%d Mbps grant #%d = %+v, %v", tc.mbps, i+1, permit, err)
			}
		}
		if ttl := rdb.TTL(context.Background(), "device_bandwidth:"+device+":upload").Val(); ttl < 59*time.Second {
			t.Errorf("bucket TTL = %v, want at least 60s before elapsed request time", ttl)
		}
	}
}

func TestDeviceBandwidthPermitDowngradeDiscardsPreviousCredit(t *testing.T) {
	rdb := newBandwidthRedis(t)
	device := bandwidthDevice(t, rdb)
	mbps := int64(100)
	db := &bandwidthAuthorizationDB{speed: &mbps}
	svc := NewDeviceBandwidthService(db, rdb)
	if permit, err := svc.Permit(context.Background(), bandwidthTestNode, device, "upload", 1); err != nil || !permit.Allowed {
		t.Fatalf("initial high-rate request = %+v, %v", permit, err)
	}
	mbps = 1
	permit, err := svc.Permit(context.Background(), bandwidthTestNode, device, "upload", 65535)
	if err != nil || permit.Allowed || permit.RetryAfterMS != 525 {
		t.Fatalf("downgraded request = %+v, %v; want denial and 525ms", permit, err)
	}
	time.Sleep(550 * time.Millisecond)
	permit, err = svc.Permit(context.Background(), bandwidthTestNode, device, "upload", 65535)
	if err != nil || !permit.Allowed {
		t.Fatalf("refill at downgraded rate = %+v, %v", permit, err)
	}
}

func TestDeviceBandwidthPermitRateChangesDoNotMintAnotherBurst(t *testing.T) {
	rdb := newBandwidthRedis(t)
	device := bandwidthDevice(t, rdb)
	mbps := int64(1)
	svc := NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, rdb)
	if permit, err := svc.Permit(context.Background(), bandwidthTestNode, device, "upload", 65535); err != nil || !permit.Allowed {
		t.Fatalf("initial burst = %+v, %v", permit, err)
	}
	for _, next := range []int64{2, 1, 2, 1} {
		mbps = next
		permit, err := svc.Permit(context.Background(), bandwidthTestNode, device, "upload", 65535)
		if err != nil || permit.Allowed || permit.RetryAfterMS < 1 || permit.RetryAfterMS > 1000 {
			t.Fatalf("rate change to %d Mbps = %+v, %v; must not mint another burst", next, permit, err)
		}
	}
}

func TestDeviceBandwidthPermitConcurrentExitsConsumeAtomically(t *testing.T) {
	rdb := newBandwidthRedis(t)
	device := bandwidthDevice(t, rdb)
	mbps := int64(1)
	exits := []struct {
		nodeID string
		svc    *DeviceBandwidthService
	}{
		{bandwidthTestNode, NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, rdb)},
		{"12465e85-b31c-4967-8a1b-8b121268d4cf", NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, rdb)},
	}
	var wg sync.WaitGroup
	results := make(chan DeviceBandwidthPermit, 16)
	for i := 0; i < 16; i++ {
		exit := exits[i%len(exits)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, err := exit.svc.Permit(context.Background(), exit.nodeID, device, "upload", 65535)
			if err != nil {
				t.Errorf("concurrent permit: %v", err)
			}
			results <- permit
		}()
	}
	wg.Wait()
	close(results)
	allowed := 0
	for permit := range results {
		if permit.Allowed {
			allowed++
		}
	}
	if allowed != 1 {
		t.Errorf("concurrent full-burst grants = %d, want exactly 1", allowed)
	}
}

func TestDeviceBandwidthPermitRedisErrorsFailClosed(t *testing.T) {
	rdb := newBandwidthRedis(t)
	device := bandwidthDevice(t, rdb)
	mbps := int64(10)
	svc := NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, rdb)
	// A wrong-type bucket makes Lua fail. Never grant on a script error.
	if err := rdb.Set(context.Background(), "device_bandwidth:"+device+":upload", "broken", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if permit, err := svc.Permit(context.Background(), bandwidthTestNode, device, "upload", 1); err == nil || permit.Allowed {
		t.Fatalf("corrupt bucket = %+v, %v; want fail-closed error", permit, err)
	}
	closed := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR"), MaxRetries: -1})
	_ = closed.Close()
	if permit, err := NewDeviceBandwidthService(&bandwidthAuthorizationDB{speed: &mbps}, closed).Permit(context.Background(), bandwidthTestNode, device, "upload", 1); err == nil || permit.Allowed {
		t.Fatalf("unavailable Redis = %+v, %v; want fail-closed error", permit, err)
	}
}
