package scheduler

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"os"
	"testing"
)

func TestConcurrencyEvictionPendingUntilAllExitsAcknowledge(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
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
	group, plan, user, target, n1, n2 := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	if _, err := db.Exec(ctx, `INSERT INTO node_groups(id,name) VALUES($1,'eviction-coverage')`, group); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM node_groups WHERE id=$1`, group) }()
	if _, err := db.Exec(ctx, `INSERT INTO plans(id,name,duration_days,max_devices,max_concurrent,node_group_id) VALUES($1,'eviction-coverage',30,3,1,$2)`, plan, group); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM plans WHERE id=$1`, plan) }()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,email,password_hash,sub_token,plan_id,status) VALUES($1,$2,'x',$3,$4,'active')`, user, user+"@test.invalid", crypto.NewUUID(), plan); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) }()
	if _, err := db.Exec(ctx, `INSERT INTO devices(user_id,xray_uuid) VALUES($1,$2)`, user, target); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{n1, n2} {
		if _, err := db.Exec(ctx, `INSERT INTO nodes(id,name,ip,role,status,api_key,last_seen,shaping_mode,shaping_ok,xray_running,config_hash) VALUES($1,'Exit','203.0.113.7','exit','online','key',NOW(),'device_global_v1',true,true,'applied')`, node); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = db.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, node) }()
	}
	obs := services.OnlineObservation{Complete: true, NewestUUID: target}
	ids, err := eligibleEvictionExits(ctx, db, obs)
	if err != nil || len(ids) != 2 {
		t.Fatalf("eligible exits %v %v", ids, err)
	}
	epoch, err := beginUUIDEviction(ctx, db, user, target, ids)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM uuid_evictions WHERE device_uuid=$1`, target) }()
	var evicted bool
	if err := db.QueryRow(ctx, `SELECT evicted_until > NOW() FROM devices WHERE xray_uuid=$1`, target).Scan(&evicted); err != nil || !evicted {
		t.Fatalf("eviction not active %v %v", evicted, err)
	}
	if pending, err := completeUUIDEvictions(ctx, db, user); err != nil || !pending {
		t.Fatalf("premature confirmation %v %v", pending, err)
	}
	if err := database.AcknowledgeUUIDEviction(ctx, db, target, epoch, ids[0]); err != nil {
		t.Fatal(err)
	}
	if pending, _ := completeUUIDEvictions(ctx, db, user); !pending {
		t.Fatal("one ack must not confirm")
	}
	if err := database.AcknowledgeUUIDEviction(ctx, db, target, epoch, ids[1]); err != nil {
		t.Fatal(err)
	}
	if pending, err := completeUUIDEvictions(ctx, db, user); err != nil || pending {
		t.Fatalf("both acks not confirmed %v %v", pending, err)
	}
}
