package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func TestAdminTunnelSessionCountUsesAuthenticatedTransports(t *testing.T) {
	db := chainTestDB(t)
	ctx := context.Background()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	// Use the DB-backed services fixture helpers to avoid asserting arbitrary
	// whole-fleet completeness against an existing shared test database.
	user, credential := uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO users(id,email,password_hash,sub_token,status) VALUES($1,$2,'test',$3,'active')`, user, user+"@test.example", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) })
	_, err = db.Exec(ctx, `INSERT INTO devices(user_id,name,xray_uuid) VALUES($1,'session-test',$2)`, user, credential)
	if err != nil {
		t.Fatal(err)
	}
	node := uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO nodes(id,name,ip,role,status,api_key,last_seen) VALUES($1,$2,'203.0.113.71','exit','online','test',NOW())`, node, "session-node-"+node)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, node)
		_ = rdb.Del(ctx, "node:"+node+":tunnels").Err()
	})
	c := nodeprov.TunnelConnection{UUID: credential, PeerIP: "198.51.100.2", PeerPort: 40000, LocalIP: "203.0.113.71", LocalPort: 8443, AcceptedAt: 100}
	other := c
	other.PeerPort++
	other.AcceptedAt++
	if err := services.PublishTunnelReport(ctx, rdb, node, nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{c, other}}); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/admin/users/:id/tunnel-sessions", NewAdminTunnelSessionHandler(db, rdb).Get)
	t.Cleanup(func() { _ = app.Shutdown() })
	read := func() map[string]any {
		t.Helper()
		response, err := app.Test(httptest.NewRequest("GET", "/admin/users/"+user+"/tunnel-sessions", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, _ := io.ReadAll(response.Body)
		var result map[string]any
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	// If another test left a registered Exit without a fresh report, this
	// endpoint intentionally returns unknown; only isolated DB can claim 2.
	result := read()
	if result["complete"] == true && result["count"] != float64(2) {
		t.Fatalf("count=%v", result)
	}
	if err := services.PublishTunnelReport(ctx, rdb, node, nodeprov.TunnelReport{}); err != nil {
		t.Fatal(err)
	}
	result = read()
	if result["complete"] != false || result["count"] != nil {
		t.Fatalf("unknown must not be zero: %v", result)
	}
}
