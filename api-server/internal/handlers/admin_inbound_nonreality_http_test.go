package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

var nonRealityProtocols = []string{"vmess_ws", "trojan_tls", "shadowsocks", "hysteria2", "wireguard"}

func TestInboundNonRealityVMessCreateKeepsCanonicalUnset(t *testing.T) {
	// Given a registered node with no canonical SNI and no configured inbounds.
	f := newInboundRealityFixture(t, nil)

	// When an enabled VMess inbound suppresses the legacy Reality fallback.
	status, body := f.request("POST", "/nodes/"+f.node+"/inbounds", `{"protocol":"vmess_ws","port":443,"tag":"test"}`)

	// Then creation succeeds without inventing a Reality SNI.
	if status != 201 {
		t.Fatalf("create = %d: %s", status, body)
	}
	var canonical *string
	if err := f.db.QueryRow(context.Background(), `SELECT reality_client_sni FROM nodes WHERE id=$1`, f.node).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	if canonical != nil {
		t.Fatalf("non-Reality creation set canonical SNI to %q", *canonical)
	}
	if _, _, status, code := f.state(); status != "not_applicable" || code != "" {
		t.Fatalf("non-Reality state = %q %q", status, code)
	}
}

func TestInboundNonRealityCreateCommitsWithoutApplicableListener(t *testing.T) {
	for _, protocol := range nonRealityProtocols {
		t.Run(protocol, func(t *testing.T) {
			// Given a canonical that differs from legacy fallback; When creating an enabled non-Reality inbound; Then it commits without rewriting SNI.
			canonical := "other.example.test"
			f := newInboundRealityFixture(t, &canonical)
			status, body := f.request("POST", "/nodes/"+f.node+"/inbounds", fmt.Sprintf(`{"protocol":%q,"port":443,"tag":"test"}`, protocol))
			if status != 201 {
				t.Fatalf("create = %d: %s", status, body)
			}
			var created inboundResponse
			if err := json.Unmarshal([]byte(body), &created); err != nil {
				t.Fatal(err)
			}
			if created.Protocol != protocol || !created.Enabled || created.NodeID != f.node {
				t.Fatalf("created inbound = %+v", created)
			}
			if protocol == "wireguard" && (created.Settings["private_key"] == "" || created.Settings["address"] == "") {
				t.Fatalf("missing generated WireGuard settings: %+v", created.Settings)
			}
			if name, source, status, code := f.state(); name != canonical || source != "admin" || status != "not_applicable" || code != "" {
				t.Fatalf("canonical changed: %q %q %q %q", name, source, status, code)
			}
		})
	}
}

func TestInboundNonRealityDisabledCreateRejectsIncompatibleFallback(t *testing.T) {
	// Given an incompatible canonical and no enabled rows; When a disabled non-Reality inbound is created; Then fallback validation rolls it back.
	canonical := "other.example.test"
	f := newInboundRealityFixture(t, &canonical)
	status, body := f.request("POST", "/nodes/"+f.node+"/inbounds", `{"protocol":"vmess_ws","port":443,"tag":"test","enabled":false}`)
	if status != 409 {
		t.Fatalf("disabled create = %d: %s", status, body)
	}
	var count int
	if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM inbounds WHERE node_id=$1`, f.node).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected create left %d inbounds", count)
	}
}

func TestInboundNonRealityCreateRejectsIncompatibleEligibleSpeedTier(t *testing.T) {
	// Given an eligible speed-tier listener; When creating a non-Reality inbound; Then its effective Reality name is validated.
	canonical := "other.example.test"
	f := newInboundRealityFixture(t, &canonical)
	var group, user string
	if err := f.db.QueryRow(context.Background(), `WITH grp AS (INSERT INTO node_groups (name) VALUES (gen_random_uuid()::text) RETURNING id), pl AS (INSERT INTO plans (name, duration_days, speed_limit, node_group_id) SELECT 'tier',30,10,id FROM grp RETURNING id), usr AS (INSERT INTO users (email,password_hash,sub_token,plan_id,status) SELECT gen_random_uuid()::text,'test',gen_random_uuid()::text,id,'active' FROM pl RETURNING id), dev AS (INSERT INTO devices (user_id,xray_uuid) SELECT id,gen_random_uuid()::text FROM usr RETURNING id) SELECT grp.id::text,usr.id::text FROM grp,usr,dev`).Scan(&group, &user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := f.db.Exec(ctx, `DELETE FROM users WHERE id=$1`, user); err != nil {
			t.Error(err)
		}
		if _, err := f.db.Exec(ctx, `DELETE FROM plans WHERE node_group_id=$1`, group); err != nil {
			t.Error(err)
		}
		if _, err := f.db.Exec(ctx, `DELETE FROM node_groups WHERE id=$1`, group); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.db.Exec(context.Background(), `INSERT INTO node_group_nodes (node_group_id,node_id) VALUES ($1,$2)`, group, f.node); err != nil {
		t.Fatal(err)
	}
	status, body := f.request("POST", "/nodes/"+f.node+"/inbounds", `{"protocol":"vmess_ws","port":443,"tag":"test"}`)
	if status != 409 {
		t.Fatalf("speed-tier create = %d: %s", status, body)
	}
	var count int
	if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM inbounds WHERE node_id=$1`, f.node).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected create left %d inbounds", count)
	}
}

func TestInboundNonRealityUpdateCommitsWithoutApplicableListener(t *testing.T) {
	for _, protocol := range nonRealityProtocols {
		t.Run(protocol, func(t *testing.T) {
			// Given an enabled non-Reality inbound; When editing its port; Then the edit commits with canonical state untouched.
			canonical := "other.example.test"
			f := newInboundRealityFixture(t, &canonical)
			id := f.seed(protocol, "", true)
			status, body := f.request("PUT", "/inbounds/"+id, `{"port":8443}`)
			if status != 200 {
				t.Fatalf("update = %d: %s", status, body)
			}
			var updated inboundResponse
			if err := json.Unmarshal([]byte(body), &updated); err != nil {
				t.Fatal(err)
			}
			if updated.Port != 8443 || updated.Protocol != protocol {
				t.Fatalf("updated inbound = %+v", updated)
			}
			if name, source, status, code := f.state(); name != canonical || source != "admin" || status != "not_applicable" || code != "" {
				t.Fatalf("canonical changed: %q %q %q %q", name, source, status, code)
			}
		})
	}
}

func TestInboundNonRealityToggleOnSuppressesFallback(t *testing.T) {
	for _, protocol := range nonRealityProtocols {
		t.Run(protocol, func(t *testing.T) {
			// Given a disabled non-Reality inbound; When enabling it suppresses fallback; Then the resulting non-applicable set commits.
			canonical := "other.example.test"
			f := newInboundRealityFixture(t, &canonical)
			id := f.seed(protocol, "", false)
			status, body := f.request("PUT", "/inbounds/"+id+"/toggle", "")
			if status != 200 {
				t.Fatalf("toggle = %d: %s", status, body)
			}
			var toggled inboundResponse
			if err := json.Unmarshal([]byte(body), &toggled); err != nil {
				t.Fatal(err)
			}
			if !toggled.Enabled {
				t.Fatal("toggle did not enable inbound")
			}
			if name, source, status, code := f.state(); name != canonical || source != "admin" || status != "not_applicable" || code != "" {
				t.Fatalf("canonical changed: %q %q %q %q", name, source, status, code)
			}
		})
	}
}

func TestInboundNonRealityFallbackTransitionsValidateAndRollback(t *testing.T) {
	for _, protocol := range nonRealityProtocols {
		t.Run(protocol, func(t *testing.T) {
			for _, action := range []struct{ name, method, path, body string }{
				{"disable", "PUT", "/inbounds/:id/toggle", ""},
				{"delete", "DELETE", "/inbounds/:id", ""},
				{"update disable", "PUT", "/inbounds/:id", `{"enabled":false,"port":8443}`},
			} {
				t.Run(action.name, func(t *testing.T) {
					// Given an enabled non-Reality inbound and incompatible fallback canonical; When it is disabled or deleted; Then the inbound remains intact.
					canonical := "other.example.test"
					f := newInboundRealityFixture(t, &canonical)
					id := f.seed(protocol, "", true)
					path := strings.ReplaceAll(action.path, ":id", id)
					status, body := f.request(action.method, path, action.body)
					if status != 409 {
						t.Fatalf("fallback transition = %d: %s", status, body)
					}
					var enabled bool
					var port int
					if err := f.db.QueryRow(context.Background(), `SELECT enabled, port FROM inbounds WHERE id=$1`, id).Scan(&enabled, &port); err != nil {
						t.Fatal(err)
					}
					if !enabled || port != 443 {
						t.Fatalf("rejected mutation persisted: enabled=%v port=%d", enabled, port)
					}
					if name, source, nodeStatus, code := f.state(); name != canonical || source != "admin" || nodeStatus != "valid" || code != "" {
						t.Fatalf("canonical changed: %q %q %q %q", name, source, nodeStatus, code)
					}
				})
			}
		})
	}
}

func TestInboundNonRealityDeleteAndToggleCommitWithCompatibleFallback(t *testing.T) {
	for _, protocol := range nonRealityProtocols {
		t.Run(protocol, func(t *testing.T) {
			for _, action := range []struct {
				name, method, suffix string
				want                 int
			}{
				{"toggle", "PUT", "/toggle", 200},
				{"delete", "DELETE", "", 204},
			} {
				t.Run(action.name, func(t *testing.T) {
					// Given a canonical accepted by fallback; When disabling or deleting the last inbound; Then the transition commits.
					canonical := "www.cloudflare.com"
					f := newInboundRealityFixture(t, &canonical)
					id := f.seed(protocol, "", true)
					status, body := f.request(action.method, "/inbounds/"+id+action.suffix, "")
					if status != action.want {
						t.Fatalf("mutation = %d: %s", status, body)
					}
					var count int
					var enabled bool
					if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*), COALESCE(BOOL_OR(enabled), false) FROM inbounds WHERE id=$1`, id).Scan(&count, &enabled); err != nil {
						t.Fatal(err)
					}
					if action.name == "delete" && count != 0 || action.name == "toggle" && (count != 1 || enabled) {
						t.Fatalf("inbound after mutation: count=%d enabled=%v", count, enabled)
					}
					if name, source, nodeStatus, code := f.state(); name != canonical || source != "admin" || nodeStatus != "valid" || code != "" {
						t.Fatalf("canonical changed: %q %q %q %q", name, source, nodeStatus, code)
					}
				})
			}
		})
	}
}
