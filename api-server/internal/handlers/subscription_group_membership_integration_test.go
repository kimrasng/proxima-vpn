package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

type groupMembershipHTTPFixture struct {
	t           *testing.T
	app         *fiber.App
	pool        *pgxpool.Pool
	groupID     string
	nodeID      string
	subPath     string
	nodeName    string
	nodeAddress string
}

func newGroupMembershipHTTPFixture(t *testing.T) groupMembershipHTTPFixture {
	t.Helper()

	pool := chainTestDB(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	nodeName := "subscription-member-" + suffix
	nodeAddress := "203.0.113.50"
	nodeID := seedChainNode(t, pool, nodeName, "exit", 443)
	if _, err := pool.Exec(ctx, `INSERT INTO inbounds (node_id, protocol, port, tag, settings) VALUES ($1,'vless_reality',443,'membership-reality','{"dest":"reality.example.test:443","server_names":["reality.example.test"]}')`, nodeID); err != nil {
		t.Fatalf("seed membership Reality listener: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE nodes
		 SET status = 'online', publish_direct = true,
		     reality_public_key = 'integration-public-key', reality_short_id = 'abcdef01',
		     reality_client_sni = 'reality.example.test', reality_sni_source = 'admin', reality_sni_status = 'valid',
		     xray_running = true, last_seen = NOW()
		 WHERE id = $1`, nodeID,
	); err != nil {
		t.Fatalf("prepare direct subscription node: %v", err)
	}

	groupID := seedChainGroup(t, pool, "subscription-membership-group-"+suffix)
	var planID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		 VALUES ($1, 30, 1, $2) RETURNING id::text`,
		"subscription-membership-plan-"+suffix, groupID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed subscription plan: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, planID); err != nil {
			t.Errorf("clean up subscription plan: %v", err)
		}
	})

	subToken := crypto.NewUUID()
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, name, password_hash, sub_token, plan_id, status, is_active, language)
		 VALUES ($1, 'subscription test', 'unused', $2, $3, 'active', true, 'en')
		 RETURNING id::text`,
		"subscription-membership-"+suffix+"@example.test", subToken, planID,
	).Scan(&userID); err != nil {
		t.Fatalf("seed subscription user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("clean up subscription user: %v", err)
		}
	})

	var deviceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid)
		 VALUES ($1, 'integration device', $2) RETURNING id::text`,
		userID, crypto.NewUUID(),
	).Scan(&deviceID); err != nil {
		t.Fatalf("seed subscription device: %v", err)
	}

	groups := NewAdminNodeGroupHandler(pool)
	subscriptions := NewSubscriptionHandler(pool, 3600)
	app := fiber.New()
	app.Put("/admin/node-groups/:id/nodes", groups.SetNodes)
	app.Get("/admin/node-groups/:id", groups.Get)
	app.Get("/sub/:sub_token/:device_id", subscriptions.GetSubscription)
	t.Cleanup(func() {
		if err := app.Shutdown(); err != nil {
			t.Errorf("shut down Fiber app: %v", err)
		}
	})

	return groupMembershipHTTPFixture{
		t:           t,
		app:         app,
		pool:        pool,
		groupID:     groupID,
		nodeID:      nodeID,
		subPath:     "/sub/" + subToken + "/" + deviceID,
		nodeName:    nodeName,
		nodeAddress: nodeAddress,
	}
}

func (f groupMembershipHTTPFixture) request(method, path string, body []byte) (int, []byte) {
	f.t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	response, err := f.app.Test(request)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			f.t.Errorf("close %s %s response: %v", method, path, err)
		}
	}()
	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return response.StatusCode, bodyBytes
}

func (f groupMembershipHTTPFixture) setNodes(body string) (int, []byte) {
	f.t.Helper()
	status, response := f.request(fiber.MethodPut, "/admin/node-groups/"+f.groupID+"/nodes", []byte(body))
	if status == fiber.StatusOK {
		digest, err := services.NewXrayConfigService(f.pool).GenerateDigest(f.t.Context(), f.nodeID)
		if err != nil {
			f.t.Fatal(err)
		}
		if _, err := f.pool.Exec(f.t.Context(), `UPDATE nodes SET config_hash=$2 WHERE id=$1`, f.nodeID, digest.Hash); err != nil {
			f.t.Fatal(err)
		}
	}
	return status, response
}

func (f groupMembershipHTTPFixture) subscription() (int, string) {
	f.t.Helper()
	status, body := f.request(fiber.MethodGet, f.subPath, nil)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		f.t.Fatalf("decode subscription body %q: %v", body, err)
	}
	return status, string(decoded)
}

func TestDirectChainVisibilityFollowsSetNodesThroughHTTP(t *testing.T) {
	fixture := newGroupMembershipHTTPFixture(t)

	status, body := fixture.setNodes(`{"node_ids":["` + fixture.nodeID + `"]}`)
	if status != fiber.StatusOK {
		t.Fatalf("add node status = %d, want 200; body=%s", status, body)
	}
	status, links := fixture.subscription()
	if status != fiber.StatusOK {
		t.Fatalf("subscription status = %d, want 200", status)
	}
	if !strings.Contains(links, "@"+fixture.nodeAddress+":443") {
		t.Fatalf("subscription %q does not advertise the seeded direct-chain address", links)
	}
	if !strings.Contains(links, fixture.nodeName) {
		t.Errorf("subscription %q does not contain seeded node name %q", links, fixture.nodeName)
	}

	status, body = fixture.setNodes(`{"node_ids":[]}`)
	if status != fiber.StatusOK {
		t.Fatalf("clear nodes status = %d, want 200; body=%s", status, body)
	}
	status, links = fixture.subscription()
	if status != fiber.StatusOK {
		t.Fatalf("subscription after clear status = %d, want 200", status)
	}
	if links != "" {
		t.Errorf("subscription after explicit empty node_ids = %q, want no direct endpoints", links)
	}
}

func TestSetNodesRejectsMissingNodeIDsWithoutChangingMembershipThroughHTTP(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "omitted", body: `{}`},
		{name: "null", body: `{"node_ids":null}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newGroupMembershipHTTPFixture(t)
			status, body := fixture.setNodes(`{"node_ids":["` + fixture.nodeID + `"]}`)
			if status != fiber.StatusOK {
				t.Fatalf("seed membership status = %d, want 200; body=%s", status, body)
			}

			status, body = fixture.setNodes(testCase.body)
			if status != fiber.StatusBadRequest {
				t.Errorf("invalid node_ids status = %d, want 400; body=%s", status, body)
			}

			status, body = fixture.request(fiber.MethodGet, "/admin/node-groups/"+fixture.groupID, nil)
			if status != fiber.StatusOK {
				t.Fatalf("group detail status = %d, want 200; body=%s", status, body)
			}
			var detail struct {
				Nodes []nodeGroupNode `json:"nodes"`
			}
			if err := json.Unmarshal(body, &detail); err != nil {
				t.Fatalf("decode group detail: %v", err)
			}
			if len(detail.Nodes) != 1 || detail.Nodes[0].ID != fixture.nodeID {
				t.Errorf("group nodes after rejected request = %+v, want seeded node %s", detail.Nodes, fixture.nodeID)
			}

			status, links := fixture.subscription()
			if status != fiber.StatusOK {
				t.Fatalf("subscription after rejected request status = %d, want 200", status)
			}
			if !strings.Contains(links, "@"+fixture.nodeAddress+":443") {
				t.Errorf("subscription after rejected request = %q, want seeded direct endpoint", links)
			}
		})
	}
}
