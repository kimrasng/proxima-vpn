package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
	"gopkg.in/yaml.v3"
)

func TestMobileDefaultSubscriptionExternalProviders(t *testing.T) {
	if os.Getenv("ROUTING_NETWORK_TESTS") == "" {
		t.Skip("requires explicit external provider test")
	}
	pool := chainTestDB(t)
	f := newSubscriptionHTTPFixture(t, pool)
	if _, err := pool.Exec(t.Context(), `DELETE FROM clash_routing_profile WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE nodes SET tags=ARRAY['kr-home','jp'] WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	handler := NewSubscriptionHandler(pool, 3600)
	app := fiber.New()
	app.Get("/sub/:sub_token", handler.GetAccountSubscription)
	path := f.path[:strings.LastIndex(f.path, "/")]
	for _, tc := range []struct{ name, ua string }{{"ios", "Proxima-iOS/1.0"}, {"android", "Proxima-Android/1.0"}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("User-Agent", tc.ua)
			resp, err := app.Test(req, 30000)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			if tc.name == "ios" {
				if strings.Contains(string(body), ".mrs") || strings.Contains(string(body), "AND,") || !strings.Contains(string(body), ".list") {
					t.Fatalf("invalid iOS profile: %s", body)
				}
				return
			}
			lines := strings.Split(string(body), "\n")
			if len(lines) < 2 {
				t.Fatalf("missing metadata: %s", body)
			}
			last := lines[len(lines)-1]
			if !strings.HasPrefix(last, "# proxima-routing-rules:") {
				t.Fatalf("missing metadata: %s", last)
			}
			metadata, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(last, "# proxima-routing-rules:"))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Rules []struct {
					IP     []string `json:"ip"`
					Domain []string `json:"domain"`
				} `json:"rules"`
			}
			if err := json.Unmarshal(metadata, &doc); err != nil {
				t.Fatal(err)
			}
			var ips, domains int
			for _, rule := range doc.Rules {
				ips += len(rule.IP)
				domains += len(rule.Domain)
			}
			if ips < 5000 || domains < 500 {
				t.Fatalf("incomplete rules: %d IPs, %d domains", ips, domains)
			}
		})
	}
}

func TestAndroidSubscriptionProjectsProcessRulesAndPreservesQUIC(t *testing.T) {
	pool := chainTestDB(t)
	f := newSubscriptionHTTPFixture(t, pool)
	profile := subscription.RoutingProfile{
		Final:  "auto",
		Groups: []subscription.RoutingGroup{{Key: "auto", Name: "PROXY", Type: "url-test", Members: []subscription.RoutingMember{{Kind: subscription.MemberAll}}}},
		Providers: []subscription.RoutingProvider{
			{Key: "process", Type: "inline", Behavior: "classical", Payload: []string{"PROCESS-NAME,game.exe"}},
			{Key: "quic", Type: "inline", Behavior: "classical", Payload: []string{"AND,((DOMAIN-SUFFIX,example.com),(NETWORK,UDP),(DST-PORT,443))"}},
		},
		Rules: []subscription.RoutingRule{{Type: "RULE-SET", Value: "process", Target: "auto"}, {Type: "RULE-SET", Value: "quic", Target: "REJECT"}},
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO clash_routing_profile(id,document) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET document=EXCLUDED.document`, data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM clash_routing_profile WHERE id=1`) })
	app := fiber.New()
	app.Get("/sub/:sub_token", NewSubscriptionHandler(pool, 3600).GetAccountSubscription)
	req := httptest.NewRequest("GET", f.path[:strings.LastIndex(f.path, "/")], nil)
	req.Header.Set("User-Agent", "Proxima-Android/1.0.0")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	lines := strings.Split(string(body), "\n")
	metadata, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(lines[len(lines)-1], "# proxima-routing-rules:"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rules []struct {
			Target  string   `json:"target"`
			Domain  []string `json:"domain"`
			Network string   `json:"network"`
			Port    string   `json:"port"`
			Match   bool     `json:"match"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(metadata, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Rules) != 2 || doc.Rules[0].Target != "block" || len(doc.Rules[0].Domain) != 1 || doc.Rules[0].Domain[0] != "domain:example.com" || doc.Rules[0].Network != "udp" || doc.Rules[0].Port != "443" || !doc.Rules[1].Match {
		t.Fatalf("unexpected routing rules: %+v", doc.Rules)
	}
}

func TestMobileSubscriptionHTTP(t *testing.T) {
	pool := chainTestDB(t)
	f := newSubscriptionHTTPFixture(t, pool)
	profile := subscription.RoutingProfile{
		BlockQUIC: true,
		Final:     "auto",
		Groups:    []subscription.RoutingGroup{{Key: "auto", Name: "PROXY", Type: "url-test", Members: []subscription.RoutingMember{{Kind: subscription.MemberAll}}}},
		Providers: []subscription.RoutingProvider{{Key: "private", Type: "inline", Behavior: "domain", Payload: []string{"+.example.com"}}},
		Rules:     []subscription.RoutingRule{{Type: "RULE-SET", Value: "private", Target: "DIRECT"}},
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO clash_routing_profile(id,document) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET document=EXCLUDED.document`, data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM clash_routing_profile WHERE id=1`) })
	handler := NewSubscriptionHandler(pool, 3600)
	handler.resolveMobile = func(_ context.Context, p subscription.RoutingProfile) (subscription.RoutingProfile, map[string][]string, error) {
		return p, map[string][]string{"private": {"+.example.com"}}, nil
	}
	handler.resolveAndroid = handler.resolveMobile
	app := fiber.New()
	app.Get("/sub/:sub_token", handler.GetAccountSubscription)
	path := f.path[:strings.LastIndex(f.path, "/")]
	for _, tc := range []struct{ name, ua string }{{"ios", "Proxima-iOS/1.0.0 (iOS 18.2)"}, {"android", "Proxima-Android/1.0.0 (Android 15)"}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("User-Agent", tc.ua)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			if tc.name == "ios" {
				var doc struct {
					Rules   []string         `yaml:"rules"`
					Proxies []map[string]any `yaml:"proxies"`
				}
				if err := yaml.Unmarshal(body, &doc); err != nil {
					t.Fatal(err)
				}
				if len(doc.Proxies) != 1 || doc.Proxies[0]["type"] != "vless" {
					t.Fatalf("proxies: %+v", doc.Proxies)
				}
				if strings.Contains(string(body), "AND,") || strings.Contains(string(body), "fake-ip") || strings.Contains(string(body), ".mrs") {
					t.Fatalf("unsupported iOS config: %s", body)
				}
				return
			}
			lines := strings.Split(string(body), "\n")
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "vless://") || !strings.HasPrefix(lines[1], "# proxima-routing-rules:") {
				t.Fatalf("unexpected content: %s", body)
			}
			metadata, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(lines[1], "# proxima-routing-rules:"))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Version int `json:"version"`
				Rules   []struct {
					Target  string   `json:"target"`
					Domain  []string `json:"domain"`
					Network string   `json:"network"`
					Port    string   `json:"port"`
					Match   bool     `json:"match"`
				} `json:"rules"`
			}
			if err := json.Unmarshal(metadata, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Version != 1 || len(doc.Rules) != 3 || doc.Rules[0].Network != "udp" || doc.Rules[0].Port != "443" || doc.Rules[1].Target != "direct" || len(doc.Rules[1].Domain) != 1 || doc.Rules[1].Domain[0] != "domain:example.com" || !doc.Rules[2].Match {
				t.Fatalf("metadata: %+v", doc)
			}
		})
	}
	handler.resolveAndroid = func(context.Context, subscription.RoutingProfile) (subscription.RoutingProfile, map[string][]string, error) {
		return subscription.RoutingProfile{}, nil, errors.New("provider failed")
	}
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("User-Agent", "Proxima-Android/1.0.0")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable || !strings.Contains(string(body), "mobile routing rules unavailable") {
		t.Fatalf("unsafe provider failure response: %d %s", resp.StatusCode, body)
	}
}
