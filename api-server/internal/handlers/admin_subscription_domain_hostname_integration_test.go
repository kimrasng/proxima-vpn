package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAdminSubscriptionDomain_createsOrdinaryHostname(t *testing.T) {
	// Given: a syntactically valid DNS hostname.
	f := newSubscriptionDomainFixture(t)

	// When: an admin creates a subscription domain.
	status, body := f.request(fiber.MethodPost, "/admin/subscription-domains", `{"domain":"normal.example.test","is_default":true}`)

	// Then: the host is persisted and selected as default.
	if status != fiber.StatusCreated {
		t.Fatalf("valid create status = %d, body = %s", status, body)
	}
	var domain domainResponse
	if err := json.Unmarshal(body, &domain); err != nil {
		t.Fatalf("decode created domain: %v", err)
	}
	if domain.Domain != "normal.example.test" || !domain.IsDefault {
		t.Fatalf("created domain = %+v", domain)
	}
	assertOnlyDefault(t, f, "normal.example.test")
	var persisted string
	if err := f.pool.QueryRow(context.Background(), `SELECT domain FROM subscription_domains WHERE id = $1`, domain.ID).Scan(&persisted); err != nil {
		t.Fatalf("read created domain: %v", err)
	}
	if persisted != "normal.example.test" {
		t.Fatalf("persisted domain = %q", persisted)
	}
}

func TestAdminSubscriptionDomain_rejectsInvalidHostnameOnCreate(t *testing.T) {
	for _, tc := range invalidSubscriptionHostnames() {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a persisted default and an invalid candidate hostname.
			f := newSubscriptionDomainFixture(t)
			seedSubscriptionDomain(t, f, "old.example.test", true)

			// When: the admin creates that candidate as the new default.
			body := fmt.Sprintf(`{"domain":%q,"is_default":true}`, tc.domain)
			status, response := f.request(fiber.MethodPost, "/admin/subscription-domains", body)

			// Then: the API rejects it before changing the previous default or inserting a row.
			if status != fiber.StatusBadRequest {
				t.Fatalf("create %q: status = %d, want 400; body = %s", tc.domain, status, response)
			}
			assertOnlyDefault(t, f, "old.example.test")
			var count int
			if err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscription_domains`).Scan(&count); err != nil {
				t.Fatalf("count domains: %v", err)
			}
			if count != 1 {
				t.Fatalf("domains after rejected create = %d, want 1", count)
			}
		})
	}
}

func TestAdminSubscriptionDomain_rejectsInvalidHostnameOnUpdate(t *testing.T) {
	for _, tc := range invalidSubscriptionHostnames() {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a default and an existing non-default target row.
			f := newSubscriptionDomainFixture(t)
			seedSubscriptionDomain(t, f, "old.example.test", true)
			id := seedSubscriptionDomain(t, f, "original.example.test", false)

			// When: an admin renames the target to an invalid hostname and selects it as default.
			body := fmt.Sprintf(`{"domain":%q,"is_default":true}`, tc.domain)
			status, response := f.request(fiber.MethodPut, "/admin/subscription-domains/"+id, body)

			// Then: the target row and previous default remain unchanged.
			if status != fiber.StatusBadRequest {
				t.Fatalf("update %q: status = %d, want 400; body = %s", tc.domain, status, response)
			}
			assertOnlyDefault(t, f, "old.example.test")
			var domain string
			var selected bool
			if err := f.pool.QueryRow(context.Background(), `SELECT domain, is_default FROM subscription_domains WHERE id = $1`, id).Scan(&domain, &selected); err != nil {
				t.Fatalf("read target after rejection: %v", err)
			}
			if domain != "original.example.test" || selected {
				t.Fatalf("target after rejected update = %q, default=%t", domain, selected)
			}
		})
	}
}

func TestAdminSubscriptionDomain_normalizesValidHostnameOnCreate(t *testing.T) {
	for _, hostname := range []string{"  MiXeD.Example.Test  ", "  XN--BCHER-KVA.Example  "} {
		t.Run(hostname, func(t *testing.T) {
			// Given: a valid mixed-case ASCII hostname, optionally in punycode.
			f := newSubscriptionDomainFixture(t)

			// When: an admin creates the domain.
			status, body := f.request(fiber.MethodPost, "/admin/subscription-domains", fmt.Sprintf(`{"domain":%q}`, hostname))

			// Then: trim and lowercase apply consistently to the stored value and response.
			if status != fiber.StatusCreated {
				t.Fatalf("valid hostname create = %d, body = %s", status, body)
			}
			var created domainResponse
			if err := json.Unmarshal(body, &created); err != nil {
				t.Fatalf("decode created hostname: %v", err)
			}
			want := strings.ToLower(strings.TrimSpace(hostname))
			if created.Domain != want {
				t.Fatalf("created hostname = %q, want %q", created.Domain, want)
			}
			var stored string
			if err := f.pool.QueryRow(context.Background(), `SELECT domain FROM subscription_domains WHERE id = $1`, created.ID).Scan(&stored); err != nil {
				t.Fatalf("read stored hostname: %v", err)
			}
			if stored != want {
				t.Fatalf("stored hostname = %q, want %q", stored, want)
			}
		})
	}
}

func TestAdminSubscriptionDomain_normalizesValidHostnameOnUpdate(t *testing.T) {
	// Given: an existing domain and default.
	f := newSubscriptionDomainFixture(t)
	seedSubscriptionDomain(t, f, "old.example.test", true)
	id := seedSubscriptionDomain(t, f, "original.example.test", false)

	// When: an admin renames and selects the target using a mixed-case punycode hostname.
	status, body := f.request(fiber.MethodPut, "/admin/subscription-domains/"+id, `{"domain":"  XN--BCHER-KVA.Example  ","is_default":true}`)

	// Then: normalized hostname and default selection are persisted together.
	if status != fiber.StatusOK {
		t.Fatalf("valid hostname update = %d, body = %s", status, body)
	}
	var updated domainResponse
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("decode updated hostname: %v", err)
	}
	if updated.Domain != "xn--bcher-kva.example" || !updated.IsDefault {
		t.Fatalf("updated hostname = %+v", updated)
	}
	assertOnlyDefault(t, f, "xn--bcher-kva.example")
}

func invalidSubscriptionHostnames() []struct{ name, domain string } {
	return []struct{ name, domain string }{
		{"explicit port", "normal.example.test:9443"},
		{"bracketed IPv6", "[2001:db8::1]"},
		{"raw IPv6", "2001:db8::1"},
		{"IPv4", "192.0.2.1"},
		{"URL", "https://normal.example.test/sub"},
		{"empty label", "normal..example.test"},
		{"leading empty label", ".example.test"},
		{"trailing empty label", "example.test."},
		{"wildcard", "*.example.test"},
		{"overlength label", strings.Repeat("a", 64) + ".example.test"},
		{"overlength hostname", strings.Repeat("ab.", 84) + "test"},
		{"leading hyphen", "-normal.example.test"},
		{"trailing hyphen", "normal-.example.test"},
		{"whitespace label", "normal. .test"},
		{"underscore", "normal_bad.example.test"},
		{"empty", "   "},
	}
}
