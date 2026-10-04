package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestPublicSubscriptionDomains_filtersAndOrdersWhenListed(t *testing.T) {
	// Given: mixed visibility and equal-order rows with explicit creation times.
	f := newSubscriptionDomainFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		domain  string
		enabled bool
		public  bool
		order   int
		created time.Time
	}{
		{"later.example.test", true, true, 2, base.Add(time.Hour)},
		{"early.example.test", true, true, 2, base},
		{"first.example.test", true, true, 1, base.Add(2 * time.Hour)},
		{"disabled.example.test", false, true, 0, base},
		{"private.example.test", true, false, 0, base},
	} {
		if _, err := f.pool.Exec(ctx, `INSERT INTO subscription_domains (domain, is_enabled, is_public, sort_order, created_at) VALUES ($1, $2, $3, $4, $5)`, row.domain, row.enabled, row.public, row.order, row.created); err != nil {
			t.Fatalf("seed domain %s: %v", row.domain, err)
		}
	}

	// When: the user lists subscription domains through HTTP.
	status, body := f.request(fiber.MethodGet, "/user/subscription-domains", "")

	// Then: only enabled public domains are returned, sorted by order and creation time.
	if status != fiber.StatusOK {
		t.Fatalf("list status = %d, body = %s", status, body)
	}
	var domains []publicDomainResponse
	if err := json.Unmarshal(body, &domains); err != nil {
		t.Fatalf("decode domains: %v", err)
	}
	if len(domains) != 3 || domains[0].Domain != "first.example.test" || domains[1].Domain != "early.example.test" || domains[2].Domain != "later.example.test" {
		t.Fatalf("public domains = %+v, want first, early, later", domains)
	}
}
