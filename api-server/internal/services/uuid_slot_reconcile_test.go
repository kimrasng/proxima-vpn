package services

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/redis/go-redis/v9"
)

func TestUUIDSlotReconciliationPreAdmissionAndIdle(t *testing.T) {
	dsn, addr := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("isolated TEST_DATABASE_URL and TEST_REDIS_ADDR required")
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
	user, device, n1, n2 := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, node := range []string{n1, n2} {
		if _, err := db.Exec(ctx, `INSERT INTO nodes(id,name,ip,role,status,api_key,last_seen) VALUES($1,'slot-test','203.0.113.1','exit','online','key',NOW())`, node); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = db.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, node) }()
	}
	defer rdb.Del(ctx, AccountUUIDSlotKey(user), AccountUUIDSlotKey(user)+":reservations", AccountUUIDSlotKey(user)+":reserved_at", AccountUUIDSlotKey(user)+":sequence", AccountUUIDSlotKey(user)+":absence:"+device)
	for _, node := range []string{n1, n2} {
		defer rdb.Del(ctx, "node:"+node+":admitted", "node:"+node+":admitted:fence", "node:"+node+":admitted:epoch")
	}
	gen1, gen2 := uuid.NewString(), uuid.NewString()
	if err := BeginExitReportGeneration(ctx, rdb, n1, gen1); err != nil {
		t.Fatal(err)
	}
	if err := BeginExitReportGeneration(ctx, rdb, n2, gen2); err != nil {
		t.Fatal(err)
	}
	for i, node := range []string{n1, n2} {
		gen := gen1
		if i == 1 {
			gen = gen2
		}
		if err := PublishAdmittedUUIDReport(ctx, rdb, node, gen, 1, []string{}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	admitted, err := ReserveAccountUUIDSlot(ctx, rdb, user, device, 1)
	if err != nil || !admitted {
		t.Fatalf("reserve: %v %v", admitted, err)
	}
	// A pre-admission empty report cannot remove the newcomer, even after time.
	if _, err := ReconcileAccountUUIDSlots(ctx, db, rdb, user); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rdb.SIsMember(ctx, AccountUUIDSlotKey(user), device).Result(); !ok {
		t.Fatal("released on old report")
	}
	// Existing idle association remains admitted indefinitely as it appears in
	// both authoritative Exit reports despite no further permit calls.
	if err := PublishAdmittedUUIDReport(ctx, rdb, n1, gen1, 2, []string{device}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := PublishAdmittedUUIDReport(ctx, rdb, n2, gen2, 2, []string{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileAccountUUIDSlots(ctx, db, rdb, user); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rdb.SIsMember(ctx, AccountUUIDSlotKey(user), device).Result(); !ok {
		t.Fatal("released idle active UUID")
	}
	// An incomplete Exit report is unknown, not proof of disconnection.
	rdb.Del(ctx, "node:"+n2+":admitted")
	if _, err := ReconcileAccountUUIDSlots(ctx, db, rdb, user); err == nil {
		t.Fatal("missing Exit accepted")
	}
}
