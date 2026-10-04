package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/middleware"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/redis/go-redis/v9"
)

// This test requires a disposable database: the eviction snapshot covers every
// serving Exit in the database, not just the nodes belonging to this account.
func TestUUIDEvictionEndToEnd(t *testing.T) {
	dsn, redisAddr := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_ADDR required (disposable PostgreSQL 17+ and Redis 7)")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var pgVersion int
	if err := db.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&pgVersion); err != nil || pgVersion < 170000 {
		t.Fatalf("PostgreSQL 17+ required: version=%d err=%v", pgVersion, err)
	}
	var existing int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname=current_schema() AND tablename='nodes'`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != 0 {
		t.Fatal("TEST_DATABASE_URL must point to a fresh isolated database (nodes table already exists)")
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()
	var redisVersion string
	info, err := rdb.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			redisVersion = strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
		}
	}
	if redisVersion == "" || redisVersion[0] < '7' {
		t.Fatalf("Redis 7+ required: version=%q", redisVersion)
	}

	group, plan, user := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	older, latest := crypto.NewUUID(), crypto.NewUUID()
	exits := []string{crypto.NewUUID(), crypto.NewUUID()}
	key := "uuid-e2e-" + crypto.NewUUID()
	defer func() {
		// Reverse-order cleanup preserves FK dependencies, including acknowledgments.
		_, _ = db.Exec(ctx, `DELETE FROM uuid_evictions WHERE device_uuid=$1`, latest)
		_, _ = db.Exec(ctx, `DELETE FROM devices WHERE user_id=$1`, user)
		_, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
		_, _ = db.Exec(ctx, `DELETE FROM plans WHERE id=$1`, plan)
		_, _ = db.Exec(ctx, `DELETE FROM node_group_nodes WHERE node_group_id=$1`, group)
		for _, id := range exits {
			_, _ = db.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, id)
		}
		_, _ = db.Exec(ctx, `DELETE FROM node_groups WHERE id=$1`, group)
		keys := []string{"account:" + user + ":online_uuids"}
		for _, id := range exits {
			keys = append(keys, "node:"+id+":online", "node:"+id+":online_ips", "node:"+id+":online_report")
		}
		for _, id := range []string{older, latest} {
			keys = append(keys, "device:"+id+":online_since", "device_bandwidth:"+id+":upload")
		}
		_ = rdb.Del(ctx, keys...).Err()
	}()
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO node_groups(id,name) VALUES($1,$2)`, group, "uuid-e2e-"+group)
	mustExec(`INSERT INTO plans(id,name,duration_days,max_devices,max_concurrent,node_group_id) VALUES($1,$2,30,2,1,$3)`, plan, "uuid-e2e-"+plan, group)
	mustExec(`INSERT INTO users(id,email,password_hash,sub_token,plan_id,status,is_active,plan_expires_at) VALUES($1,$2,'unused',$3,$4,'active',true,NOW()+INTERVAL '1 day')`, user, user+"@example.test", crypto.NewUUID(), plan)
	for _, id := range []string{older, latest} {
		mustExec(`INSERT INTO devices(user_id,xray_uuid) VALUES($1,$2)`, user, id)
	}
	for _, id := range exits {
		mustExec(`INSERT INTO nodes(id,name,ip,role,status,api_key,last_seen,shaping_mode,shaping_ok,xray_running,config_hash) VALUES($1,$2,'203.0.113.7','exit','online',$3,NOW(),'device_global_v1',true,true,'applied')`, id, "uuid-e2e-"+id, key)
		mustExec(`INSERT INTO node_group_nodes(node_group_id,node_id) VALUES($1,$2)`, group, id)
	}

	tracker := services.NewOnlineTracker(rdb)
	publish := func(uuids []string) {
		t.Helper()
		ips := map[string][]map[string]any{}
		for _, id := range uuids {
			ips[id] = []map[string]any{{"ip": "192.0.2.1", "last_seen": time.Now().Unix()}}
		}
		for _, node := range exits {
			if err := tracker.PublishOnlineReport(ctx, node, uuids, ips); err != nil {
				t.Fatal(err)
			}
		}
	}
	publish([]string{older})
	// Seed the earlier observation time without waiting for a wall-clock tick.
	if err := tracker.ObserveSessionStarts(ctx, user, []string{older}, time.Now().Add(-3*time.Second)); err != nil {
		t.Fatal(err)
	}
	first, err := tracker.ObserveAccount(ctx, db, user)
	if err != nil || !first.Complete || first.NewestUUID != older {
		t.Fatalf("initial online observation: %+v, %v", first, err)
	}
	publish([]string{older, latest})
	second, err := tracker.ObserveAccount(ctx, db, user)
	if err != nil || !second.Complete || second.NewestUUID != latest || second.Starts[latest] <= second.Starts[older] {
		t.Fatalf("distinct online transitions not observed: %+v, %v", second, err)
	}

	t.Setenv("ENFORCE_CONCURRENCY", "1")
	t.Setenv("CONCURRENCY_GRACE", "0")
	t.Setenv("CONCURRENCY_STRIKES", "2")
	s := NewConcurrencyScheduler(db, rdb)
	account := capRow{userID: user, email: user + "@example.test", cap: 1}
	s.checkUser(ctx, account)
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM uuid_evictions WHERE device_uuid=$1`, latest).Scan(&count); err != nil || count != 0 {
		t.Fatalf("first sweep must only record a strike: count=%d err=%v", count, err)
	}
	s.checkUser(ctx, account)
	var epoch, status string
	var required []string
	var confirmedAt *time.Time
	if err := db.QueryRow(ctx, `SELECT epoch::text,status,required_node_ids::text[],confirmed_at FROM uuid_evictions WHERE device_uuid=$1`, latest).Scan(&epoch, &status, &required, &confirmedAt); err != nil {
		t.Fatalf("latest UUID not selected: %v", err)
	}
	if status != "pending" || confirmedAt != nil || len(required) != 2 {
		t.Fatalf("pending fleet snapshot: status=%q confirmed=%v required=%v", status, confirmedAt, required)
	}
	for _, node := range exits {
		if required[0] != node && required[1] != node {
			t.Fatalf("Exit %s missing from snapshot %v", node, required)
		}
	}
	assertBan := func(wantPending bool) {
		t.Helper()
		var until time.Time
		if err := db.QueryRow(ctx, `SELECT evicted_until FROM devices WHERE xray_uuid=$1`, latest).Scan(&until); err != nil {
			t.Fatal(err)
		}
		if wantPending && until.Year() != 9999 {
			t.Fatalf("cooldown began before confirmation: %v", until)
		}
		if !wantPending && (until.Year() == 9999 || until.Before(time.Now().Add(9*time.Minute)) || until.After(time.Now().Add(11*time.Minute))) {
			t.Fatalf("confirmed cooldown not applied: %v", until)
		}
	}
	assertBan(true)

	app := fiber.New()
	h := handlers.NewNodeAgentHandler(db, rdb, services.ManagedEntryDNSIntentConfig{})
	auth := middleware.NodeAPIKeyMiddleware(db)
	app.Get("/nodes/:id/revoked-devices", auth, h.RevokedDevices)
	app.Post("/nodes/:id/revoked-devices/ack", auth, h.AcknowledgeRevocation)
	app.Post("/nodes/:id/bandwidth/permit", auth, h.BandwidthPermit)
	defer app.Shutdown()
	request := func(method, node, suffix, body string) []byte {
		t.Helper()
		req := httptest.NewRequest(method, "/nodes/"+node+suffix, strings.NewReader(body))
		req.Header.Set("X-Node-Key", key)
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || (res.StatusCode != 200 && res.StatusCode != 204) {
			t.Fatalf("%s %s: status=%d body=%s err=%v", method, suffix, res.StatusCode, data, err)
		}
		return data
	}
	for _, node := range exits {
		var snapshot struct {
			Revoked     []string `json:"revoked_uuids"`
			Revocations []struct {
				UUID  string `json:"uuid"`
				Epoch string `json:"epoch"`
			} `json:"revocations"`
		}
		if err := json.Unmarshal(request("GET", node, "/revoked-devices", ""), &snapshot); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, revocation := range snapshot.Revocations {
			found = found || revocation.UUID == latest && revocation.Epoch == epoch
			if revocation.UUID == older {
				t.Fatal("unaffected UUID appears in revocations")
			}
		}
		if !found || len(snapshot.Revoked) != 1 || snapshot.Revoked[0] != latest {
			t.Fatalf("Exit %s revocation snapshot: %+v", node, snapshot)
		}
	}
	permit := func(node, device string, allowed bool) {
		t.Helper()
		var response struct {
			Allowed bool `json:"allowed"`
		}
		body := `{"device_uuid":"` + device + `","direction":"upload","bytes":1}`
		if err := json.Unmarshal(request("POST", node, "/bandwidth/permit", body), &response); err != nil {
			t.Fatal(err)
		}
		if response.Allowed != allowed {
			t.Fatalf("device %s permit allowed=%t, want %t", device, response.Allowed, allowed)
		}
	}
	permit(exits[0], latest, false)
	permit(exits[0], older, true)
	ack := func(node string) {
		t.Helper()
		request("POST", node, "/revoked-devices/ack", `{"uuid":"`+latest+`","epoch":"`+epoch+`"}`)
	}
	ack(exits[0])
	s.checkUser(ctx, account) // A pending eviction cannot start another or start its cooldown.
	if err := db.QueryRow(ctx, `SELECT status,confirmed_at FROM uuid_evictions WHERE device_uuid=$1`, latest).Scan(&status, &confirmedAt); err != nil || status != "pending" || confirmedAt != nil {
		t.Fatalf("one acknowledgment confirmed eviction: %q %v %v", status, confirmedAt, err)
	}
	assertBan(true)
	ack(exits[1])
	if err := db.QueryRow(ctx, `SELECT status,confirmed_at FROM uuid_evictions WHERE device_uuid=$1`, latest).Scan(&status, &confirmedAt); err != nil || status != "confirmed" || confirmedAt == nil {
		t.Fatalf("second acknowledgment did not confirm: %q %v %v", status, confirmedAt, err)
	}
	assertBan(true) // Confirmation records the time; scheduler applies the finite cooldown.
	if pending, err := completeUUIDEvictions(ctx, db, user); err != nil || pending {
		t.Fatalf("completion after both acknowledgments: pending=%t err=%v", pending, err)
	}
	assertBan(false)
	permit(exits[1], latest, false)
	permit(exits[1], older, true)
}
