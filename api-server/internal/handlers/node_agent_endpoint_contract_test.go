package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestRegisterPreservesManagedEndpointIntentAndCanonicalSNIThroughHTTP(t *testing.T) {
	for _, attached := range []bool{true, false} {
		t.Run(map[bool]string{true: "live", false: "detached"}[attached], func(t *testing.T) {
			pool := chainTestDB(t)
			id, token := seedPendingRegistrationNode(t, pool)
			ctx := context.Background()
			var intentID string
			hostname := "entry-" + crypto.NewUUID() + ".example.test"
			cleanup := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
			var linked *string
			if attached {
				linked = &id
			}
			if err := pool.QueryRow(ctx, `INSERT INTO managed_entry_dns
				(owner_node_id, node_id, hostname, desired_action, dns_status, cleanup_requested_at)
				VALUES ($1, $2, $3, 'delete', 'deleting', $4) RETURNING id::text`,
				id, linked, hostname, cleanup).Scan(&intentID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM managed_entry_dns WHERE id=$1`, intentID) })
			if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni='www.cloudflare.com', reality_sni_source='admin', reality_sni_status='valid' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			app := fiber.New()
			app.Post("/nodes/register", NewNodeAgentHandler(pool, nil, services.ManagedEntryDNSIntentConfig{}).Register)
			t.Cleanup(func() { _ = app.Shutdown() })

			// When the pending node registers, its managed intent and SNI are not agent-owned.
			status, err := registrationRequest(app, token, 8443)
			if err != nil || status != 200 {
				t.Fatalf("registration status=%d error=%v", status, err)
			}

			var actualID, actualOwner, actualHost, action, dnsStatus string
			var actualLinked *string
			var actualCleanup time.Time
			if err := pool.QueryRow(ctx, `SELECT id::text, owner_node_id::text, node_id::text, hostname,
				desired_action, dns_status, cleanup_requested_at FROM managed_entry_dns WHERE id=$1`, intentID).
				Scan(&actualID, &actualOwner, &actualLinked, &actualHost, &action, &dnsStatus, &actualCleanup); err != nil {
				t.Fatal(err)
			}
			if actualID != intentID || actualOwner != id || actualHost != hostname || action != "delete" || dnsStatus != "deleting" || !actualCleanup.Equal(cleanup) {
				t.Errorf("managed intent changed: id=%s owner=%s hostname=%s action=%s status=%s cleanup=%v", actualID, actualOwner, actualHost, action, dnsStatus, actualCleanup)
			}
			if (actualLinked == nil) != (linked == nil) || (actualLinked != nil && *actualLinked != id) {
				t.Errorf("live association changed: %v", actualLinked)
			}
			var sni, source, sniStatus string
			if err := pool.QueryRow(ctx, `SELECT reality_client_sni, reality_sni_source, reality_sni_status FROM nodes WHERE id=$1`, id).Scan(&sni, &source, &sniStatus); err != nil {
				t.Fatal(err)
			}
			if sni != "www.cloudflare.com" || source != "admin" || sniStatus != "valid" {
				t.Errorf("SNI changed: %q %q %q", sni, source, sniStatus)
			}
		})
	}
}
