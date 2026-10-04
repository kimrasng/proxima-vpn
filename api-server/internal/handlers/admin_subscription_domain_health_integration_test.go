package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAdminSubscriptionDomainHealth_classifiesDNSFailureWithValidStatuses(t *testing.T) {
	// Given: a domain that cannot be resolved as a DNS name.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "bad host", false)

	// When: an admin checks domain health.
	status, body := f.request(fiber.MethodGet, "/admin/subscription-domains/health", "")

	// Then: all three statuses are meaningful and DNS failure is reported.
	if status != fiber.StatusOK {
		t.Fatalf("health status = %d, body = %s", status, body)
	}
	var results []domainHealthResponse
	if err := json.Unmarshal(body, &results); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if len(results) != 1 || results[0].DNSStatus != "failed" || results[0].TLSStatus != "unknown" || results[0].CertificateStatus != "unknown" || results[0].Error == "" {
		t.Fatalf("DNS failure health = %+v", results)
	}
}

func TestAdminSubscriptionDomainHealth_classifiesTLSFailureWithValidStatuses(t *testing.T) {
	// Given: localhost resolves, but no trusted TLS endpoint is available on port 443.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "127.0.0.1", false)

	// When: an admin checks domain health.
	status, body := f.request(fiber.MethodGet, "/admin/subscription-domains/health", "")

	// Then: a failed TLS connection also classifies certificate status.
	if status != fiber.StatusOK {
		t.Fatalf("health status = %d, body = %s", status, body)
	}
	var results []domainHealthResponse
	if err := json.Unmarshal(body, &results); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if len(results) != 1 || results[0].DNSStatus != "healthy" || results[0].TLSStatus != "failed" || results[0].CertificateStatus != "unknown" || results[0].Error == "" {
		t.Fatalf("TLS failure health = %+v", results)
	}
}

func TestAdminSubscriptionDomainHealth_surfacesPersistenceFailure(t *testing.T) {
	// Given: a database trigger rejecting health writes for the isolated fixture.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "bad host", false)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `CREATE FUNCTION reject_subscription_health_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture health persistence failure'; END $$`); err != nil {
		t.Fatalf("install failure trigger function: %v", err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(ctx, `DROP FUNCTION reject_subscription_health_write() CASCADE`); err != nil {
			t.Errorf("remove failure trigger: %v", err)
		}
	})
	if _, err := f.pool.Exec(ctx, `CREATE TRIGGER reject_subscription_health BEFORE UPDATE ON subscription_domains FOR EACH ROW EXECUTE FUNCTION reject_subscription_health_write()`); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	// When: an admin requests a check that cannot be persisted.
	status, body := f.request(fiber.MethodGet, "/admin/subscription-domains/health", "")

	// Then: the response is an error, not a misleading success.
	if status != fiber.StatusInternalServerError {
		t.Fatalf("failed health persistence status = %d, want 500; body = %s", status, body)
	}
}
