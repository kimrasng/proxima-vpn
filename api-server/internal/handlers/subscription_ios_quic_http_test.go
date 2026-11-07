package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
	"gopkg.in/yaml.v3"
)

func TestIOSSubscriptionOmitsQUICProviderAndRetainsRoutingHTTP(t *testing.T) {
	pool := chainTestDB(t)
	f := newSubscriptionHTTPFixture(t, pool)
	profile := subscription.RoutingProfile{
		BlockQUIC: true,
		Final:     "auto",
		Groups:    []subscription.RoutingGroup{{Key: "auto", Name: "PROXY", Type: "url-test", Members: []subscription.RoutingMember{{Kind: subscription.MemberAll}}}},
		Providers: []subscription.RoutingProvider{
			{Key: "quic", Type: "inline", Behavior: "classical", Payload: []string{"AND,((DOMAIN-SUFFIX,openai.com),(NETWORK,UDP),(DST-PORT,443))"}},
			{Key: "ordinary", Type: "inline", Behavior: "classical", Payload: []string{"DOMAIN-SUFFIX,example.com"}},
		},
		Rules: []subscription.RoutingRule{
			{Type: "RULE-SET", Value: "quic", Target: "REJECT"},
			{Type: "RULE-SET", Value: "ordinary", Target: "DIRECT"},
		},
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO clash_routing_profile(id,document) VALUES(1,$1) ON CONFLICT(id) DO UPDATE SET document=EXCLUDED.document`, data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM clash_routing_profile WHERE id=1`); err != nil {
			t.Errorf("clean up routing profile: %v", err)
		}
	})
	app := fiber.New()
	app.Get("/sub/:sub_token", NewSubscriptionHandler(pool, 3600).GetAccountSubscription)
	t.Cleanup(func() {
		if err := app.Shutdown(); err != nil {
			t.Errorf("shut down Fiber app: %v", err)
		}
	})
	req := httptest.NewRequest(http.MethodGet, f.path[:strings.LastIndex(f.path, "/")], nil)
	req.Header.Set("User-Agent", "Proxima-iOS/1.0.0 (iOS 18.2)")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var doc struct {
		Proxies []struct {
			Type   string `yaml:"type"`
			Server string `yaml:"server"`
			Port   int    `yaml:"port"`
			UDP    bool   `yaml:"udp"`
		} `yaml:"proxies"`
		Providers map[string]struct {
			Type     string   `yaml:"type"`
			Behavior string   `yaml:"behavior"`
			Payload  []string `yaml:"payload"`
		} `yaml:"rule-providers"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Proxies) != 1 {
		t.Fatalf("expected one fixture proxy, got %+v", doc.Proxies)
	}
	proxy := doc.Proxies[0]
	if proxy.Type != "vless" || proxy.Server != f.entryHost || proxy.Port != f.entryPort || proxy.UDP {
		t.Fatalf("expected TCP VLESS at %s:%d, got %+v", f.entryHost, f.entryPort, proxy)
	}
	ordinary, ok := doc.Providers["ordinary"]
	if !ok || len(doc.Providers) != 1 || ordinary.Type != "inline" || ordinary.Behavior != "classical" || !reflect.DeepEqual(ordinary.Payload, []string{"DOMAIN-SUFFIX,example.com"}) {
		t.Fatalf("expected only retained ordinary provider, got %+v", doc.Providers)
	}
	if want := []string{"RULE-SET,ordinary,DIRECT", "MATCH,PROXY"}; !reflect.DeepEqual(doc.Rules, want) {
		t.Fatalf("expected rules %v, got %v", want, doc.Rules)
	}
}
