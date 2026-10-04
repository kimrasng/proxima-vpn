package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestRegisterRefreshesExistingManagedEntryIntentThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	id, token := seedPendingRegistrationNode(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET role='relay' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	hostname := crypto.NewUUID() + ".example.test"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_entry_dns
		(owner_node_id, node_id, hostname, cloudflare_zone_id, desired_ipv4, desired_action, dns_status)
		VALUES ($1,$1,$2,$3,'203.0.113.90'::inet,'present','ready')`, id, hostname, managedEntryTestConfig.ZoneID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, id); err != nil {
			t.Errorf("clean up intent: %v", err)
		}
	})
	app := fiber.New()
	app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, managedEntryTestConfig).Register)
	t.Cleanup(func() { _ = app.Shutdown() })

	status, err := registrationRequest(app, token, 8443)
	if err != nil || status != fiber.StatusOK {
		t.Fatalf("registration status=%d error=%v, want 200", status, err)
	}

	var actualHostname, ip, dnsStatus string
	var generation int
	if err := pool.QueryRow(ctx, `SELECT hostname, host(desired_ipv4), dns_status, generation
		FROM managed_entry_dns WHERE owner_node_id=$1`, id).Scan(&actualHostname, &ip, &dnsStatus, &generation); err != nil {
		t.Fatal(err)
	}
	if actualHostname != hostname || ip != "203.0.113.91" || dnsStatus != "pending" || generation != 2 {
		t.Errorf("refreshed intent hostname=%s ip=%s status=%s generation=%d", actualHostname, ip, dnsStatus, generation)
	}
}

func TestRegisterRollsBackWhenManagedEntryIntentPersistenceFailsThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	id, token := seedPendingRegistrationNode(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET role='relay' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO managed_entry_dns
		(owner_node_id, node_id, hostname, cloudflare_zone_id, desired_ipv4, desired_action, dns_status)
		VALUES ($1,$1,$2,$3,'203.0.113.90'::inet,'present','ready')`,
		id, crypto.NewUUID()+".example.test", managedEntryTestConfig.ZoneID); err != nil {
		t.Fatal(err)
	}
	constraint := "phs029_reject_" + strings.ReplaceAll(crypto.NewUUID(), "-", "")
	if _, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE managed_entry_dns ADD CONSTRAINT %s
		CHECK (owner_node_id <> '%s'::uuid OR desired_ipv4 IS DISTINCT FROM '203.0.113.91'::inet)`, constraint, id)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `ALTER TABLE managed_entry_dns DROP CONSTRAINT `+constraint); err != nil {
			t.Errorf("drop test constraint: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, id); err != nil {
			t.Errorf("clean up intent: %v", err)
		}
	})
	app := fiber.New()
	app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, managedEntryTestConfig).Register)
	t.Cleanup(func() { _ = app.Shutdown() })

	status, err := registrationRequest(app, token, 8443)
	if err != nil || status != fiber.StatusInternalServerError {
		t.Fatalf("registration status=%d error=%v, want 500", status, err)
	}

	var state, storedToken string
	if err := pool.QueryRow(ctx, `SELECT status, reg_token FROM nodes WHERE id=$1`, id).Scan(&state, &storedToken); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || storedToken != token {
		t.Errorf("failed registration node status=%q token=%q, want pending and original token", state, storedToken)
	}
	var chains int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM node_chains WHERE exit_node_id=$1`, id).Scan(&chains); err != nil {
		t.Fatal(err)
	}
	if chains != 0 {
		t.Errorf("chains after rolled-back registration=%d, want 0", chains)
	}
}

func TestRegisterDoesNotCreateManagedEntryIntentForExitThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	id, token := seedPendingRegistrationNode(t, pool)
	app := fiber.New()
	app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, managedEntryTestConfig).Register)
	t.Cleanup(func() { _ = app.Shutdown() })

	status, err := registrationRequest(app, token, 8443)
	if err != nil || status != fiber.StatusOK {
		t.Fatalf("registration status=%d error=%v, want 200", status, err)
	}

	var intents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_entry_dns WHERE owner_node_id=$1`, id).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 0 {
		t.Errorf("exit node intent count=%d, want 0", intents)
	}
}
