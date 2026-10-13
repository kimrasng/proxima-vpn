package handlers_test

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/server"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

func TestSourceAdmissionHTTP(t *testing.T) {
	fixture := newExitRulesHTTPFixture(t)
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	app := server.NewServer(&config.Config{}, fixture.pool, rdb, nil, nil).App()
	t.Cleanup(func() { _ = app.Shutdown() })
	suffix := crypto.NewUUID()
	node := fixture.seedNode("source-exit-"+suffix, "exit", "203.0.113.70")
	fixture.seedNode("source-relay-"+suffix, "relay", "203.0.113.71")
	fixture.seedNode("source-both-"+suffix, "both", "203.0.113.72")
	group := fixture.seedGroup("source-group-" + suffix)
	fixture.addPoolMember(group, node)
	var plan, user string
	if err := fixture.pool.QueryRow(ctx, `INSERT INTO plans(name,duration_days,max_devices,max_concurrent,node_group_id) VALUES($1,30,1,1,$2) RETURNING id::text`, "source-plan-"+suffix, group).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = fixture.pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, plan) })
	if err := fixture.pool.QueryRow(ctx, `INSERT INTO users(email,password_hash,sub_token,plan_id,status,is_active) VALUES($1,'unused',$2,$3,'active',true) RETURNING id::text`, suffix+"@example.test", crypto.NewUUID(), plan).Scan(&user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = fixture.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) })
	device, second := crypto.NewUUID(), crypto.NewUUID()
	for _, d := range []string{device, second} {
		if _, err := fixture.pool.Exec(ctx, `INSERT INTO devices(user_id,name,xray_uuid) VALUES($1,'source-test',$2)`, user, d); err != nil {
			t.Fatal(err)
		}
	}
	addressKey, uuidKey := services.AddressSlotKey(user), services.AccountUUIDSlotKey(user)
	t.Cleanup(func() {
		_ = rdb.Del(ctx, addressKey, addressKey+":sequence", addressKey+":absence", uuidKey, uuidKey+":sequence", uuidKey+":reservations", uuidKey+":reserved_at").Err()
	})
	request := func(d, source, key string, status int, message string) {
		t.Helper()
		body := fmt.Sprintf(`{"device_uuid":%q,"source_address":%q}`, d, source)
		req := httptest.NewRequest(fiber.MethodPost, "/api/v1/nodes/"+node+"/devices/admit", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-Key", key)
		// Client-provided identity must not influence the configured cohort.
		req.Header.Set("x-hwid", crypto.NewUUID())
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != status || !strings.Contains(string(data), message) {
			t.Fatalf("source %q: status=%d body=%s want=%d %q", source, res.StatusCode, data, status, message)
		}
	}
	request(device, "192.0.2.1", "", 401, "")
	request(device, "192.0.2.1", "test", 204, "")
	for _, body := range []string{
		fmt.Sprintf(`{"device_uuid":%q,"session_token":%q}`, device, strings.Repeat("a", 64)),
		fmt.Sprintf(`{"device_uuid":%q,"learn_only":true,"relay_peer":{"ip":"203.0.113.2","port":40000,"local_port":8443}}`, device),
	} {
		req := httptest.NewRequest(fiber.MethodPost, "/api/v1/nodes/"+node+"/devices/admit", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-Key", "test")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 503 {
			t.Fatalf("session request without fresh Exit evidence status=%d body=%s", res.StatusCode, data)
		}
	}
	if err := fixture.pool.QueryRow(ctx, `UPDATE plans SET max_concurrent=1 WHERE id=$1 RETURNING id`, plan).Scan(new(string)); err != nil {
		t.Fatal(err)
	}
	firstConnection := nodeprov.TunnelConnection{UUID: device, PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 1000}
	secondConnection := firstConnection
	secondConnection.PeerPort++
	secondConnection.AcceptedAt++
	reportedFirst, reportedSecond := firstConnection, secondConnection
	reportedFirst.UUID, reportedSecond.UUID = strings.ToUpper(device), strings.ToUpper(device)
	if err := services.PublishTunnelReport(ctx, rdb, node, nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{reportedFirst, reportedSecond}}); err != nil {
		t.Fatal(err)
	}
	sessionToken, err := nodeprov.TunnelSessionToken(node, firstConnection)
	if err != nil {
		t.Fatal(err)
	}
	admitSession := func(token string) (int, string) {
		t.Helper()
		body := fmt.Sprintf(`{"device_uuid":%q,"session_token":%q}`, device, token)
		req := httptest.NewRequest(fiber.MethodPost, "/api/v1/nodes/"+node+"/devices/admit", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-Key", "test")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(data)
	}
	if status, body := admitSession(sessionToken); status != 204 {
		t.Fatalf("first session: status=%d body=%s", status, body)
	}
	if status, body := admitSession(sessionToken); status != 204 {
		t.Fatalf("repeat admission of same transport: status=%d body=%s", status, body)
	}
	secondToken, err := nodeprov.TunnelSessionToken(node, secondConnection)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := admitSession(secondToken); status != 409 || !strings.Contains(body, "online session limit reached") {
		t.Fatalf("N+1 session: status=%d body=%s", status, body)
	}
	if status, body := admitSession(strings.Repeat("a", 64)); status != 503 || !strings.Contains(body, "session admission unavailable") {
		t.Fatalf("unknown session: status=%d body=%s", status, body)
	}
	sessionSlots := services.TunnelSlotKey(user)
	t.Cleanup(func() {
		_ = rdb.Del(ctx, sessionSlots, sessionSlots+":sequence", sessionSlots+":absence", sessionSlots+":epoch").Err()
	})
	if count := rdb.SCard(ctx, sessionSlots).Val(); count != 1 {
		t.Fatalf("same transport repeat admission counted twice: session slot count=%d", count)
	}
	request(device, "::ffff:192.0.2.1", "test", 204, "")
	request(device, "192.0.2.2", "test", 409, "online address limit reached")
	if members, err := rdb.SMembers(ctx, addressKey).Result(); err != nil || len(members) != 1 || members[0] != "192.0.2.1" {
		t.Fatalf("canonical slots=%v err=%v", members, err)
	}
	if count := rdb.ZCard(ctx, uuidKey).Val(); count != 0 {
		t.Fatal("source admission consumed UUID slots")
	}
	for _, source := range []string{"203.0.113.71", "::ffff:203.0.113.71", "203.0.113.72", "0.0.0.0", "invalid"} {
		request(device, source, "test", 503, "admission unavailable")
	}
	// Zero means unlimited, but still reserves observed addresses.
	if _, err := fixture.pool.Exec(ctx, `UPDATE plans SET max_concurrent=0 WHERE id=$1`, plan); err != nil {
		t.Fatal(err)
	}
	request(device, "2001:db8::1", "test", 204, "")
	if _, err := fixture.pool.Exec(ctx, `UPDATE plans SET max_concurrent=-1 WHERE id=$1`, plan); err != nil {
		t.Fatal(err)
	}
	request(device, "192.0.2.3", "test", 503, "admission unavailable")
	if _, err := fixture.pool.Exec(ctx, `UPDATE plans SET max_concurrent=1 WHERE id=$1`, plan); err != nil {
		t.Fatal(err)
	}
	// Legacy admission remains UUID based and does not require a source.
	request(device, "", "test", 204, "")
	request(second, "", "test", 409, "online UUID limit reached")
	// Direct service calls must also fail closed without Redis.
	if allowed, err := services.NewDeviceBandwidthService(fixture.pool, nil).AdmitSourceDevice(ctx, node, device, "192.0.2.1"); allowed || err == nil {
		t.Fatalf("missing Redis: allowed=%v err=%v", allowed, err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE users SET plan_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	request(device, "192.0.2.1", "test", 409, "online address limit reached")
	if _, err := fixture.pool.Exec(ctx, `UPDATE users SET plan_expires_at=NULL WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	request(device, "192.0.2.1", "test", 409, "online address limit reached")
}
