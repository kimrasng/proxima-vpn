package handlers

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/redis/go-redis/v9"
)

func TestInboundMixedNodeCannotRemoveLastConfiguredRealityListener(t *testing.T) {
	pool := chainTestDB(t)
	if _, err := pool.Exec(context.Background(), `DROP INDEX IF EXISTS idx_inbounds_one_protocol_per_node`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Migrate(context.Background(), pool); err != nil {
			t.Error(err)
		}
	})
	for _, action := range []struct {
		name, method, body string
	}{
		{"delete", "DELETE", ""},
		{"toggle off", "PUT", ""},
		{"update disable", "PUT", `{"enabled":false,"port":9443}`},
		{"update protocol", "PUT", `{"protocol":"vmess_ws","port":9443}`},
	} {
		t.Run(action.name, func(t *testing.T) {
			// Given a legacy mixed node whose only configured Reality listener is enabled.
			canonical := "reality.example.test"
			f := newInboundRealityFixture(t, &canonical)
			id := f.seed("vless_reality", `"reality.example.test"`, true)
			if _, err := f.db.Exec(context.Background(), `INSERT INTO inbounds (node_id, protocol, port, tag, settings, enabled) VALUES ($1,'vmess_ws',8443,'legacy','{}',true)`, f.node); err != nil {
				t.Fatal(err)
			}
			path := "/inbounds/" + id
			if action.name == "toggle off" {
				path += "/toggle"
			}

			// When the target Reality listener is removed, disabled, or converted.
			status, body := f.request(action.method, path, action.body)

			// Then the mutation is rejected and both inbound and node state roll back.
			if status != 409 {
				t.Fatalf("%s = %d: %s", action.name, status, body)
			}
			var protocol string
			var port int
			var enabled bool
			if err := f.db.QueryRow(context.Background(), `SELECT protocol, port, enabled FROM inbounds WHERE id=$1`, id).Scan(&protocol, &port, &enabled); err != nil {
				t.Fatal(err)
			}
			if protocol != "vless_reality" || port != 443 || !enabled {
				t.Fatalf("Reality inbound changed: %q %d %v", protocol, port, enabled)
			}
			if name, source, nodeStatus, code := f.state(); name != canonical || source != "admin" || nodeStatus != "valid" || code != "" {
				t.Fatalf("node state changed: %q %q %q %q", name, source, nodeStatus, code)
			}
		})
	}
}

func TestInboundNonRealityReadModelShowsNotApplicable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		canonical *string
	}{
		{"unset canonical", nil},
		{"retained canonical", func() *string { value := "other.example.test"; return &value }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a registered node with a nil or existing canonical SNI.
			f := newInboundRealityFixture(t, tc.canonical)
			rdb := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR")})
			t.Cleanup(func() { _ = rdb.Close() })
			f.app.Get("/nodes/:id", NewAdminNodeHandler(f.db, rdb, "").GetNode)

			// When VMess replaces the implicit Reality fallback.
			status, body := f.request(fiber.MethodPost, "/nodes/"+f.node+"/inbounds", `{"protocol":"vmess_ws","port":443,"tag":"test"}`)
			if status != fiber.StatusCreated {
				t.Fatalf("create = %d: %s", status, body)
			}
			status, body = f.request(fiber.MethodGet, "/nodes/"+f.node, "")

			// Then the node read model exposes not-applicable without changing canonical SNI.
			if status != fiber.StatusOK {
				t.Fatalf("read node = %d: %s", status, body)
			}
			var node nodeEndpointFields
			if err := json.Unmarshal([]byte(body), &node); err != nil {
				t.Fatal(err)
			}
			if node.RealitySNIStatus == nil || *node.RealitySNIStatus != "not_applicable" || node.RealitySNIErrorCode != nil {
				t.Fatalf("read SNI status = %+v", node)
			}
			if tc.canonical == nil && node.RealityClientSNI != nil || tc.canonical != nil && (node.RealityClientSNI == nil || *node.RealityClientSNI != *tc.canonical) {
				t.Fatalf("read SNI canonical = %+v, want %v", node.RealityClientSNI, tc.canonical)
			}
		})
	}
}

func TestInboundNonRealityRuntimeAndMigrationConvergeOnUnsetSNI(t *testing.T) {
	// Given fresh registered nodes without a canonical SNI or source.
	runtime := newInboundRealityFixture(t, nil)
	migrated := newInboundRealityFixture(t, nil)

	// When VMess is created through HTTP on one node and backfilled on the other.
	status, body := runtime.request(fiber.MethodPost, "/nodes/"+runtime.node+"/inbounds", `{"protocol":"vmess_ws","port":443,"tag":"test"}`)
	if status != fiber.StatusCreated {
		t.Fatalf("runtime create = %d: %s", status, body)
	}
	migrated.seed("vmess_ws", "", true)
	if err := database.Migrate(context.Background(), migrated.db); err != nil {
		t.Fatal(err)
	}

	// Then both paths persist the same null-pair not-applicable state after migration.
	for _, f := range []inboundRealityFixture{runtime, migrated} {
		var canonical, source, state, code *string
		if err := f.db.QueryRow(context.Background(), `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, f.node).Scan(&canonical, &source, &state, &code); err != nil {
			t.Fatal(err)
		}
		if canonical != nil || source != nil || state == nil || *state != "not_applicable" || code != nil {
			t.Fatalf("node %s SNI state = %v %v %v %v", f.node, canonical, source, state, code)
		}
	}
}
