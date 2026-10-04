package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

var managedEntryTestConfig = services.ManagedEntryDNSIntentConfig{
	Enabled: true, ZoneID: "0123456789abcdef0123456789abcdef", BaseDomain: "example.test",
}

func TestRegisterCreatesManagedEntryIntentForForwardingNodeThroughHTTP(t *testing.T) {
	for _, role := range []string{"relay", "both"} {
		t.Run(role, func(t *testing.T) {
			pool := chainTestDB(t)
			ctx := context.Background()
			id, token := seedPendingRegistrationNode(t, pool)
			if _, err := pool.Exec(ctx, `UPDATE nodes SET role=$2 WHERE id=$1`, id, role); err != nil {
				t.Fatal(err)
			}
			app := fiber.New()
			app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, managedEntryTestConfig).Register)
			t.Cleanup(func() { _ = app.Shutdown() })
			t.Cleanup(func() {
				if _, err := pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, id); err != nil {
					t.Errorf("clean up intent: %v", err)
				}
			})

			status, err := registrationRequest(app, token, 8443)
			if err != nil || status != fiber.StatusOK {
				t.Fatalf("registration status=%d error=%v, want 200", status, err)
			}

			var owner, linked, action, dnsStatus, hostname, ip, zone string
			if err := pool.QueryRow(ctx, `SELECT owner_node_id::text, node_id::text, desired_action, dns_status,
				hostname, host(desired_ipv4), cloudflare_zone_id
				FROM managed_entry_dns WHERE owner_node_id=$1`, id).Scan(&owner, &linked, &action, &dnsStatus, &hostname, &ip, &zone); err != nil {
				t.Fatalf("read managed entry intent: %v", err)
			}
			if owner != id || linked != id || action != "present" || dnsStatus != "pending" ||
				ip != "203.0.113.91" || zone != managedEntryTestConfig.ZoneID || !strings.HasSuffix(hostname, ".example.test") {
				t.Errorf("intent owner=%s linked=%s action=%s status=%s hostname=%s ip=%s zone=%s", owner, linked, action, dnsStatus, hostname, ip, zone)
			}
		})
	}
}

func TestExplicitNodeRemovalTombstonesManagedEntryIntentThroughHTTP(t *testing.T) {
	for _, route := range []string{"admin", "agent"} {
		t.Run(route, func(t *testing.T) {
			pool := chainTestDB(t)
			ctx := context.Background()
			id := seedChainNode(t, pool, "dns-delete-"+crypto.NewUUID(), "relay", 443)
			if _, err := pool.Exec(ctx, `INSERT INTO managed_entry_dns
				(owner_node_id, node_id, hostname, desired_action, dns_status)
				VALUES ($1,$1,$2,'present','pending')`, id, crypto.NewUUID()+".example.test"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, id); err != nil {
					t.Errorf("clean up intent: %v", err)
				}
			})
			app := fiber.New()
			path := "/admin/nodes/" + id
			if route == "admin" {
				app.Delete("/admin/nodes/:id", NewAdminNodeHandler(pool, nil, "").DeleteNode)
			} else {
				path = "/nodes/" + id
				app.Delete("/nodes/:id", func(c *fiber.Ctx) error {
					c.Locals("node_id", c.Params("id"))
					return c.Next()
				}, NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{}).Unregister)
			}
			t.Cleanup(func() { _ = app.Shutdown() })
			fixture := nodeChainHTTPFixture{t: t, pool: pool, app: app}

			status, body := fixture.request(fiber.MethodDelete, path, "")
			if status != fiber.StatusOK {
				t.Fatalf("delete status=%d, want 200: %s", status, body)
			}

			var linked *string
			var action, dnsStatus string
			if err := pool.QueryRow(ctx, `SELECT node_id::text, desired_action, dns_status
				FROM managed_entry_dns WHERE owner_node_id=$1`, id).Scan(&linked, &action, &dnsStatus); err != nil {
				t.Fatalf("read tombstone: %v", err)
			}
			if linked != nil || action != "delete" || dnsStatus != "deleting" {
				t.Errorf("tombstone linked=%v action=%s status=%s", linked, action, dnsStatus)
			}
		})
	}
}

func TestAdminDeleteRetainsMissingAndEntryLinkConflictThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	app := fiber.New()
	app.Delete("/admin/nodes/:id", NewAdminNodeHandler(pool, nil, "").DeleteNode)
	t.Cleanup(func() { _ = app.Shutdown() })
	fixture := nodeChainHTTPFixture{t: t, pool: pool, app: app}
	entryID := seedChainNode(t, pool, "dns-entry-"+crypto.NewUUID(), "relay", 443)
	exitID := seedChainNode(t, pool, "dns-exit-"+crypto.NewUUID(), "exit", 443)
	if _, err := pool.Exec(ctx, `INSERT INTO node_chains
		(name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		VALUES ($1,$2,'entry.example.test',29929,$3,443,'tcp')`, "linked-"+crypto.NewUUID(), entryID, exitID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO managed_entry_dns (owner_node_id, node_id, desired_action, dns_status)
		VALUES ($1,$1,'present','pending')`, entryID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, entryID); err != nil {
			t.Errorf("clean up intent: %v", err)
		}
	})

	status, body := fixture.request(fiber.MethodDelete, "/admin/nodes/"+entryID, "")
	if status != fiber.StatusConflict || string(body) != `{"error":"remove the entry node's links before deleting it"}` {
		t.Errorf("linked delete status=%d body=%s", status, body)
	}
	var action, dnsStatus string
	if err := pool.QueryRow(ctx, `SELECT desired_action, dns_status FROM managed_entry_dns WHERE owner_node_id=$1`, entryID).
		Scan(&action, &dnsStatus); err != nil {
		t.Fatal(err)
	}
	if action != "present" || dnsStatus != "pending" {
		t.Errorf("failed delete intent action=%s status=%s, want present/pending", action, dnsStatus)
	}
	status, body = fixture.request(fiber.MethodDelete, "/admin/nodes/"+crypto.NewUUID(), "")
	if status != fiber.StatusNotFound || string(body) != `{"error":"node not found"}` {
		t.Errorf("missing delete status=%d body=%s", status, body)
	}
}

func TestAgentUnregisterReturnsNotFoundForMissingNodeThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	app := fiber.New()
	app.Delete("/nodes/:id", func(c *fiber.Ctx) error {
		c.Locals("node_id", c.Params("id"))
		return c.Next()
	}, NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{}).Unregister)
	t.Cleanup(func() { _ = app.Shutdown() })
	fixture := nodeChainHTTPFixture{t: t, pool: pool, app: app}

	status, body := fixture.request(fiber.MethodDelete, "/nodes/"+crypto.NewUUID(), "")
	if status != fiber.StatusNotFound || string(body) != `{"error":"node not found"}` {
		t.Errorf("missing unregister status=%d body=%s", status, body)
	}
}
