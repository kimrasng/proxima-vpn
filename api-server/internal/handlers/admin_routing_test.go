package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services/ruleset"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
)

func routingTestApp(t *testing.T) (*fiber.App, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed routing profile tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM clash_routing_profile`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM clash_routing_profile`) })

	h := NewAdminRoutingHandler(pool)
	app := fiber.New()
	app.Get("/routing-profile", h.Get)
	app.Put("/routing-profile", h.Update)
	app.Delete("/routing-profile", h.Reset)
	app.Post("/routing-profile/preview", h.Preview)
	app.Get("/rule-sets/contents", h.RuleSetContents)
	app.Get("/node-tags", h.NodeTags)
	return app, pool
}

func routingCall(t *testing.T, app *fiber.App, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	res, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func TestRoutingProfileDefaultUpdateReset(t *testing.T) {
	app, _ := routingTestApp(t)

	status, body := routingCall(t, app, "GET", "/routing-profile", nil)
	var got routingProfileResponse
	if status != 200 || json.Unmarshal(body, &got) != nil || !got.IsDefault || got.UpdatedAt != nil {
		t.Fatalf("initial GET = %d %s", status, body)
	}

	edited := subscription.DefaultRoutingProfile()
	edited.Rules = append(edited.Rules, subscription.RoutingRule{Type: "DOMAIN-SUFFIX", Value: "example.kr", Target: "kr_service"})
	status, body = routingCall(t, app, "PUT", "/routing-profile", edited)
	if status != 200 || json.Unmarshal(body, &got) != nil || got.IsDefault || got.UpdatedAt == nil {
		t.Fatalf("PUT = %d %s", status, body)
	}
	last := got.Profile.Rules[len(got.Profile.Rules)-1]
	if last.Value != "example.kr" {
		t.Fatalf("stored rules lost the edit: %+v", last)
	}

	status, body = routingCall(t, app, "DELETE", "/routing-profile", nil)
	if status != 200 || json.Unmarshal(body, &got) != nil || !got.IsDefault || len(got.Profile.Rules) != len(subscription.DefaultRoutingProfile().Rules) {
		t.Fatalf("DELETE = %d %s", status, body)
	}
}

func TestRoutingProfileRejectsInvalidAndKeepsStored(t *testing.T) {
	app, _ := routingTestApp(t)

	stored := subscription.DefaultRoutingProfile()
	stored.BlockQUIC = false
	if status, body := routingCall(t, app, "PUT", "/routing-profile", stored); status != 200 {
		t.Fatalf("seed PUT = %d %s", status, body)
	}

	bad := subscription.DefaultRoutingProfile()
	bad.Rules[0].Target = "missing"
	bad.Groups[0].Name = "a,b"
	status, body := routingCall(t, app, "PUT", "/routing-profile", bad)
	var verr routingValidationResponse
	if status != 400 || json.Unmarshal(body, &verr) != nil {
		t.Fatalf("invalid PUT = %d %s", status, body)
	}
	paths := map[string]bool{}
	for _, f := range verr.Fields {
		paths[f.Path] = true
	}
	if !paths["rules[0].target"] || !paths["groups[0].name"] {
		t.Fatalf("missing field paths: %+v", verr.Fields)
	}

	_, body = routingCall(t, app, "GET", "/routing-profile", nil)
	var got routingProfileResponse
	if json.Unmarshal(body, &got) != nil || got.IsDefault || got.Profile.BlockQUIC {
		t.Fatalf("stored profile changed after rejected PUT: %s", body)
	}

	status, _ = routingCall(t, app, "POST", "/routing-profile/preview", bad)
	if status != 400 {
		t.Fatalf("invalid preview = %d", status)
	}
}

func TestRoutingProfilePreviewHidesCredentials(t *testing.T) {
	app, _ := routingTestApp(t)
	status, body := routingCall(t, app, "POST", "/routing-profile/preview", subscription.DefaultRoutingProfile())
	var out struct {
		YAML string `json:"yaml"`
	}
	if status != 200 || json.Unmarshal(body, &out) != nil || !strings.Contains(out.YAML, "rule-providers:") {
		t.Fatalf("preview = %d %s", status, body)
	}
	if strings.Contains(out.YAML, "reality-opts") || strings.Contains(out.YAML, "uuid:") {
		t.Fatalf("preview must not carry real credentials:\n%s", out.YAML)
	}
}

func TestRuleSetContentsRejectsNonMirrorHosts(t *testing.T) {
	app, _ := routingTestApp(t)
	for _, u := range []string{
		"http://cdn.jsdelivr.net/gh/x/y@z/a.mrs",
		"https://169.254.169.254/latest/meta-data",
		"https://example.com/a.list",
		"https://user:pw@cdn.jsdelivr.net/gh/x/y@z/a.mrs",
	} {
		status, body := routingCall(t, app, "GET", "/rule-sets/contents?url="+url.QueryEscape(u), nil)
		if status != 400 {
			t.Errorf("%s -> %d %s, want 400", u, status, body)
		}
	}
}

func TestRuleSetContentsDecodesMetaCubeX(t *testing.T) {
	if os.Getenv("ROUTING_NETWORK_TESTS") == "" {
		t.Skip("ROUTING_NETWORK_TESTS not set; skipping test that downloads from jsDelivr")
	}
	app, _ := routingTestApp(t)
	u := subscription.MetaRulesBase + "/geosite/naver.mrs"
	status, body := routingCall(t, app, "GET", "/rule-sets/contents?url="+url.QueryEscape(u), nil)
	var out ruleset.Contents
	if status != 200 || json.Unmarshal(body, &out) != nil {
		t.Fatalf("contents = %d %s", status, body)
	}
	if out.Behavior != "domain" || out.Count == 0 || !slices.Contains(out.Entries, "+.naver.com") {
		t.Fatalf("unexpected contents: behavior=%s count=%d first=%v", out.Behavior, out.Count, out.Entries[:min(3, len(out.Entries))])
	}
}

func TestNodeTagsCountsTaggedNodes(t *testing.T) {
	app, pool := routingTestApp(t)
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, api_key, ip, port, role, status, tags)
		 VALUES ('tag-test', 'tag-test-key', '203.0.113.90'::inet, 443, 'exit', 'offline', '{zz-test-tag}')
		 RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id = $1`, id) })
	status, body := routingCall(t, app, "GET", "/node-tags", nil)
	var tags []nodeTagCount
	if status != 200 || json.Unmarshal(body, &tags) != nil {
		t.Fatalf("node-tags = %d %s", status, body)
	}
	if !slices.ContainsFunc(tags, func(x nodeTagCount) bool { return x.Tag == "zz-test-tag" && x.Nodes == 1 }) {
		t.Fatalf("zz-test-tag missing from %+v", tags)
	}
}
