package services

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestUUIDSlotReconciliationReleasesDisconnectedAfterTwoFreshEpochs(t *testing.T) {
	dsn, addr := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("isolated DB Redis required")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = rdb.Close() }()
	user, device, node, gen := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO nodes(id,name,ip,role,status,api_key,last_seen) VALUES($1,'slot-release','203.0.113.2','exit','online','key',NOW())`, node)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, node) }()
	key := AccountUUIDSlotKey(user)
	defer rdb.Del(ctx, key, key+":sequence", key+":reservations", key+":reserved_at", key+":absence:"+device, "node:"+node+":admitted", "node:"+node+":admitted:fence", "node:"+node+":admitted:epoch")
	if err := BeginExitReportGeneration(ctx, rdb, node, gen); err != nil {
		t.Fatal(err)
	}
	if err := PublishAdmittedUUIDReport(ctx, rdb, node, gen, 1, []string{device}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if allowed, err := ReserveAccountUUIDSlot(ctx, rdb, user, device, 1); err != nil || !allowed {
		t.Fatal(err)
	}
	if _, err := ReconcileAccountUUIDSlots(ctx, db, rdb, user); err != nil {
		t.Fatal(err)
	}
	if err := PublishAdmittedUUIDReport(ctx, rdb, node, gen, 2, []string{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileAccountUUIDSlots(ctx, db, rdb, user); err != nil {
		t.Fatal(err)
	}
	// Move the first absent sample back by 31s while keeping a current report;
	// this tests the duration gate without a slow wall-clock sleep.
	rdb.HSet(ctx, key+":absence:"+device, "since", time.Now().Add(-31*time.Second).UnixMilli())
	if err := PublishAdmittedUUIDReport(ctx, rdb, node, gen, 3, []string{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	released, err := ReconcileAccountUUIDSlots(ctx, db, rdb, user)
	if err != nil || len(released) != 1 || released[0] != device {
		t.Fatalf("release %v err %v", released, err)
	}
	if ok, _ := rdb.SIsMember(ctx, key, device).Result(); ok {
		t.Fatal("disconnected slot retained")
	}
}
