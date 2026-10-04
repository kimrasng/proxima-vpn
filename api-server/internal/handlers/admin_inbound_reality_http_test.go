package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

type inboundRealityFixture struct {
	t    *testing.T
	db   *pgxpool.Pool
	app  *fiber.App
	node string
}

func newInboundRealityFixture(t *testing.T, canonical *string) inboundRealityFixture {
	t.Helper()
	db := chainTestDB(t)
	node := seedChainNode(t, db, "inbound-reality-"+crypto.NewUUID(), "exit", 443)
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM inbounds WHERE node_id=$1`, node) })
	if _, err := db.Exec(context.Background(), `UPDATE nodes SET reality_client_sni=$2, reality_sni_source=CASE WHEN $2::text IS NULL THEN NULL ELSE 'admin' END, reality_sni_status=CASE WHEN $2::text IS NULL THEN NULL ELSE 'valid' END WHERE id=$1`, node, canonical); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	h := NewAdminInboundHandler(db)
	app.Post("/nodes/:nodeId/inbounds", h.Create)
	app.Put("/inbounds/:id", h.Update)
	app.Put("/inbounds/:id/toggle", h.Toggle)
	app.Delete("/inbounds/:id", h.Delete)
	t.Cleanup(func() { _ = app.Shutdown() })
	return inboundRealityFixture{t: t, db: db, app: app, node: node}
}

func (f inboundRealityFixture) request(method, path, body string) (int, string) {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := f.app.Test(r)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	return response.StatusCode, string(payload)
}

func (f inboundRealityFixture) seed(protocol, names string, enabled bool) string {
	f.t.Helper()
	var id string
	settings := fmt.Sprintf(`{"server_names":[%s]}`, names)
	if err := f.db.QueryRow(context.Background(), `INSERT INTO inbounds (node_id, protocol, port, tag, settings, enabled) VALUES ($1,$2,443,'test',$3,$4) RETURNING id::text`, f.node, protocol, settings, enabled).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f inboundRealityFixture) state() (string, string, string, string) {
	f.t.Helper()
	var name, source, status, code *string
	if err := f.db.QueryRow(context.Background(), `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, f.node).Scan(&name, &source, &status, &code); err != nil {
		f.t.Fatal(err)
	}
	value := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return value(name), value(source), value(status), value(code)
}

func TestInboundRealityCreateRepairsAndRejectsIncompatibleMutation(t *testing.T) {
	f := newInboundRealityFixture(t, nil)
	// Given a node without a canonical name; When creating a Reality inbound; Then the actual listener proposes it.
	status, body := f.request("POST", "/nodes/"+f.node+"/inbounds", `{"protocol":"vless_reality","port":443,"tag":"test","settings":{"server_names":["a.example.test"]}}`)
	if status != 201 {
		t.Fatalf("create = %d: %s", status, body)
	}
	if name, source, _, _ := f.state(); name != "a.example.test" || source != "backfill" {
		t.Fatalf("canonical = %q %q", name, source)
	}
	var created inboundResponse
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	// Given the canonical; When changing configured names incompatibly; Then both rows remain unchanged.
	status, body = f.request("PUT", "/inbounds/"+created.ID, `{"settings":{"server_names":["b.example.test"]},"port":8443}`)
	if status != 409 {
		t.Fatalf("incompatible update = %d: %s", status, body)
	}
	var port int
	if err := f.db.QueryRow(context.Background(), `SELECT port FROM inbounds WHERE id=$1`, created.ID).Scan(&port); err != nil {
		t.Fatal(err)
	}
	if port != 443 {
		t.Fatalf("rolled back port = %d", port)
	}
	var names string
	if err := f.db.QueryRow(context.Background(), `SELECT settings->>'server_names' FROM inbounds WHERE id=$1`, created.ID).Scan(&names); err != nil {
		t.Fatal(err)
	}
	if names != `["a.example.test"]` {
		t.Fatalf("rolled back settings = %s", names)
	}
	if name, _, _, _ := f.state(); name != "a.example.test" {
		t.Fatalf("rolled back canonical = %q", name)
	}
}

func TestInboundRealityToggleAndDeleteRejectLegacyFallbackMismatch(t *testing.T) {
	canonical := "a.example.test"
	f := newInboundRealityFixture(t, &canonical)
	id := f.seed("vless_reality", `"a.example.test"`, true)
	for _, action := range []struct{ method, path string }{{"PUT", "/inbounds/" + id + "/toggle"}, {"DELETE", "/inbounds/" + id}} {
		// Given a canonical that excludes the fallback; When disabling or deleting the sole inbound; Then reject without mutation.
		status, body := f.request(action.method, action.path, "")
		if status != 409 {
			t.Fatalf("%s = %d: %s", action.method, status, body)
		}
		var enabled bool
		if err := f.db.QueryRow(context.Background(), `SELECT enabled FROM inbounds WHERE id=$1`, id).Scan(&enabled); err != nil || !enabled {
			t.Fatalf("inbound changed: %v %v", enabled, err)
		}
	}
}

func TestInboundRealityMutationHandlesEffectiveSetAndFailures(t *testing.T) {
	canonical := "www.cloudflare.com"
	for _, tc := range []struct {
		name, protocol, names, method, path, body string
		want                                      int
	}{
		{"create compatible", "", "", "POST", "/nodes/:node/inbounds", `{"protocol":"vless_reality","port":443,"tag":"test","settings":{"server_names":["www.cloudflare.com"]}}`, 201},
		{"create incompatible", "", "", "POST", "/nodes/:node/inbounds", `{"protocol":"vless_reality","port":443,"tag":"test","settings":{"server_names":["other.example.test"]}}`, 409},
		{"invalid configured name", "", "", "POST", "/nodes/:node/inbounds", `{"protocol":"vless_reality","port":443,"tag":"test","settings":{"server_names":["not a host"]}}`, 409},
		{"update suppresses reality", "vless_reality", `"www.cloudflare.com"`, "PUT", "/inbounds/:id", `{"protocol":"vmess_ws"}`, 409},
		{"update activates reality", "vmess_ws", `"www.cloudflare.com"`, "PUT", "/inbounds/:id", `{"protocol":"vless_reality"}`, 200},
		{"toggle activates reality", "vless_reality", `"www.cloudflare.com"`, "PUT", "/inbounds/:id/toggle", "", 200},
		{"toggle activates mismatch", "vless_reality", `"other.example.test"`, "PUT", "/inbounds/:id/toggle", "", 409},
		{"delete activates fallback", "vless_reality", `"www.cloudflare.com"`, "DELETE", "/inbounds/:id", "", 204},
		{"missing inbound", "", "", "DELETE", "/inbounds/:id", "", 404},
		{"malformed request", "", "", "POST", "/nodes/:node/inbounds", `{bad`, 400},
		{"missing node", "", "", "POST", "/nodes/:id/inbounds", `{"protocol":"vless_reality","port":443,"tag":"test"}`, 404},
		{"malformed node ID", "", "", "POST", "/nodes/not-a-uuid/inbounds", `{"protocol":"vless_reality","port":443,"tag":"test"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a node and optional inbound; When the HTTP mutation executes; Then its status and canonical state reflect the actual listener set.
			f := newInboundRealityFixture(t, &canonical)
			id := crypto.NewUUID()
			if tc.protocol != "" {
				id = f.seed(tc.protocol, tc.names, tc.name != "toggle activates reality" && tc.name != "toggle activates mismatch")
			}
			path := strings.ReplaceAll(strings.ReplaceAll(tc.path, ":node", f.node), ":id", id)
			status, body := f.request(tc.method, path, tc.body)
			if status != tc.want {
				t.Fatalf("status = %d, want %d: %s", status, tc.want, body)
			}
			if name, _, _, _ := f.state(); name != canonical {
				t.Fatalf("canonical = %q, want %q", name, canonical)
			}
			if tc.protocol == "" && status == 409 {
				var count int
				if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM inbounds WHERE node_id=$1`, f.node).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("rejected create left %d inbounds", count)
				}
			}
			if tc.name == "update suppresses reality" {
				var protocol string
				if err := f.db.QueryRow(context.Background(), `SELECT protocol FROM inbounds WHERE id=$1`, id).Scan(&protocol); err != nil {
					t.Fatal(err)
				}
				if protocol != "vless_reality" {
					t.Fatalf("rejected protocol change persisted %s", protocol)
				}
			}
		})
	}
}

func TestInboundRealityCreateReturnsSafeErrorWhenDatabaseUnavailable(t *testing.T) {
	// Given a closed database pool; When creating an inbound; Then the response is a safe server error.
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	app := fiber.New()
	app.Post("/nodes/:nodeId/inbounds", NewAdminInboundHandler(pool).Create)
	defer app.Shutdown()
	r := httptest.NewRequest("POST", "/nodes/"+crypto.NewUUID()+"/inbounds", strings.NewReader(`{"protocol":"vless_reality","port":443,"tag":"test"}`))
	r.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 500 {
		t.Fatalf("database failure = %d", response.StatusCode)
	}
}
