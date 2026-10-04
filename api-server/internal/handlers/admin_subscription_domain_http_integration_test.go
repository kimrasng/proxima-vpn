package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func seedSubscriptionDomain(t *testing.T, f subscriptionDomainFixture, domain string, isDefault bool) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO subscription_domains (domain, is_default) VALUES ($1, $2) RETURNING id::text`,
		domain, isDefault,
	).Scan(&id); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	return id
}

func assertOnlyDefault(t *testing.T, f subscriptionDomainFixture, domain string) {
	t.Helper()
	var actual string
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COALESCE(MAX(domain) FILTER (WHERE is_default), ''), COUNT(*) FILTER (WHERE is_default) FROM subscription_domains`,
	).Scan(&actual, &count); err != nil {
		t.Fatalf("read persisted default: %v", err)
	}
	if count != 1 || actual != domain {
		t.Fatalf("persisted default = %q (count %d), want %q (count 1)", actual, count, domain)
	}
}

func TestAdminSubscriptionDomain_rejectsMalformedCreateBody(t *testing.T) {
	// Given: an admin create route with malformed JSON input.
	app := fiber.New()
	app.Post("/admin/subscription-domains", NewAdminSubscriptionDomainHandler(nil).Create)
	req := httptest.NewRequest(fiber.MethodPost, "/admin/subscription-domains", strings.NewReader(`{"domain":`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	// When: the malformed request reaches the HTTP handler.
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("malformed create request: %v", err)
	}
	defer resp.Body.Close()

	// Then: parsing rejects it before any database mutation.
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("malformed create status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminSubscriptionDomain_preservesDefaultWhenDuplicateCreateFails(t *testing.T) {
	// Given: an old default and a different existing domain.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "old.example.test", true)
	seedSubscriptionDomain(t, f, "duplicate.example.test", false)

	// When: creating the duplicate as default fails.
	status, body := f.request(fiber.MethodPost, "/admin/subscription-domains", `{"domain":"duplicate.example.test","is_default":true}`)

	// Then: the request conflicts without changing the persisted default.
	if status != fiber.StatusConflict {
		t.Fatalf("duplicate create status = %d, body = %s", status, body)
	}
	assertOnlyDefault(t, f, "old.example.test")
}

func TestAdminSubscriptionDomain_preservesDefaultWhenMissingUpdateFails(t *testing.T) {
	// Given: an old default and an absent target.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "old.example.test", true)

	// When: selecting the missing target as default.
	status, body := f.request(fiber.MethodPut, "/admin/subscription-domains/"+crypto.NewUUID(), `{"is_default":true}`)

	// Then: the request is not found and the old default remains.
	if status != fiber.StatusNotFound {
		t.Fatalf("missing update status = %d, body = %s", status, body)
	}
	assertOnlyDefault(t, f, "old.example.test")
}

func TestAdminSubscriptionDomain_preservesDefaultWhenConflictingUpdateFails(t *testing.T) {
	// Given: an old default and two different rows.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "old.example.test", true)
	seedSubscriptionDomain(t, f, "taken.example.test", false)
	targetID := seedSubscriptionDomain(t, f, "target.example.test", false)

	// When: renaming the target to a duplicate while selecting it as default.
	status, body := f.request(fiber.MethodPut, "/admin/subscription-domains/"+targetID, `{"domain":"taken.example.test","is_default":true}`)

	// Then: the conflict leaves the previous default in place.
	if status != fiber.StatusConflict {
		t.Fatalf("conflicting update status = %d, body = %s", status, body)
	}
	assertOnlyDefault(t, f, "old.example.test")
}

func TestAdminSubscriptionDomain_selectsOnlyOneDefaultWhenUpdatedSequentially(t *testing.T) {
	// Given: three existing domains and an initial default.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "old.example.test", true)
	seedSubscriptionDomain(t, f, "middle.example.test", false)
	targetID := seedSubscriptionDomain(t, f, "latest.example.test", false)

	// When: selecting another domain as default.
	status, body := f.request(fiber.MethodPut, "/admin/subscription-domains/"+targetID, `{"is_default":true}`)

	// Then: exactly the selected domain is the default.
	if status != fiber.StatusOK {
		t.Fatalf("select default status = %d, body = %s", status, body)
	}
	var response domainResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode updated domain: %v", err)
	}
	if !response.IsDefault {
		t.Fatal("updated domain response is not default")
	}
	assertOnlyDefault(t, f, "latest.example.test")
}

func TestAdminSubscriptionDomain_selectsAtMostOneDefaultWhenUpdatedConcurrently(t *testing.T) {
	// Given: two candidates and a previous default.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "old.example.test", true)
	first := seedSubscriptionDomain(t, f, "first.example.test", false)
	second := seedSubscriptionDomain(t, f, "second.example.test", false)
	statuses := make(chan int, 2)
	errors := make(chan error, 2)

	// When: both candidates are selected concurrently through HTTP.
	for _, id := range []string{first, second} {
		go func() {
			req := httptest.NewRequest(fiber.MethodPut, "/admin/subscription-domains/"+id, strings.NewReader(`{"is_default":true}`))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			resp, err := f.app.Test(req)
			if err != nil {
				errors <- err
				return
			}
			if err := resp.Body.Close(); err != nil {
				errors <- err
				return
			}
			statuses <- resp.StatusCode
		}()
	}
	for range 2 {
		select {
		case err := <-errors:
			t.Fatalf("concurrent default selection: %v", err)
		case status := <-statuses:
			if status != fiber.StatusOK {
				t.Fatalf("concurrent default selection status = %d, want 200", status)
			}
		}
	}

	// Then: precisely one of the candidates is the persisted default.
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscription_domains WHERE is_default AND id IN ($1, $2)`, first, second).Scan(&count); err != nil {
		t.Fatalf("count selected defaults: %v", err)
	}
	if count != 1 {
		t.Fatalf("selected default count = %d, want 1", count)
	}
}
