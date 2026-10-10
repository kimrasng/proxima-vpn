package handlers

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/yaml.v3"
)

// These requests exercise the real account URL, not just the User-Agent
// classifier: the same device and eligible VLESS path must produce the native
// configuration for each client, without requiring an app-specific x-hwid.
func TestAccountSubscriptionUserAgentResponses(t *testing.T) {
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	f.app.Get("/sub/:sub_token", NewSubscriptionHandler(f.pool, 3600).GetAccountSubscription)
	accountPath := f.path[:strings.LastIndex(f.path, "/")]
	accountUUIDs := func() []string {
		t.Helper()
		rows, err := f.pool.Query(t.Context(), `SELECT xray_uuid FROM devices WHERE user_id =
 (SELECT id FROM users WHERE sub_token = $1) AND retired_at IS NULL ORDER BY created_at, id`, strings.TrimPrefix(accountPath, "/sub/"))
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var uuids []string
		for rows.Next() {
			var uuid string
			if err := rows.Scan(&uuid); err != nil {
				t.Fatal(err)
			}
			uuids = append(uuids, uuid)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return uuids
	}
	tests := []struct {
		name, path, ua, accept, contentType, kind string
	}{
		{"clash meta", accountPath, "Clash-Verge/v2.0.3", "*/*", "text/yaml", "clash-meta"},
		{"mihomo", accountPath, "mihomo/1.18.0", "*/*", "text/yaml", "clash-meta"},
		{"sing-box", accountPath, "SFA/1.10.0", "*/*", "application/json", "sing-box"},
		{"hiddify", accountPath, "HiddifyNext/2.5.7", "*/*", "application/json", "sing-box"},
		{"v2ray", accountPath, "v2rayNG/1.9.16", "text/html", "text/plain", "v2ray"},
		{"unknown with html accept", accountPath, "unknown-subscription-client/1.0", "text/html", "text/plain", "v2ray"},
		{"browser", accountPath, "Mozilla/5.0 (Macintosh)", "text/html", "text/html", "html"},
		{"unknown with explicit v2ray query", accountPath + "?format=v2ray", "unknown-client/1.0", "text/html", "text/plain", "v2ray"},
		{"path overrides browser", accountPath + "/sing-box", "Mozilla/5.0 (Macintosh)", "text/html", "application/json", "sing-box"},
		{"explicit meta", accountPath + "/clash-meta", "unknown-subscription-client/1.0", "text/html", "text/yaml", "clash-meta"},
		{"explicit v2ray", accountPath + "/v2ray", "SFA/1.10.0", "*/*", "text/plain", "v2ray"},
		{"legacy query", accountPath + "?format=clash", "v2rayNG/1.9.16", "*/*", "text/yaml", "clash-meta"},
		{"legacy clash cannot import reality", accountPath + "/clash", "ClashforWindows/0.20.39", "*/*", "", "no-ready"},
		{"surfboard cannot import reality", accountPath, "Surfboard/2.24", "*/*", "", "no-ready"},
		{"quantumult cannot import reality", accountPath + "/quantumult", "unknown-client/1.0", "*/*", "", "no-ready"},
	}
	var firstUUID string
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(fiber.MethodGet, tc.path, nil)
			req.Header.Set(fiber.HeaderUserAgent, tc.ua)
			req.Header.Set(fiber.HeaderAccept, tc.accept)
			resp, err := f.app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if tc.kind == "no-ready" {
				if resp.StatusCode != fiber.StatusServiceUnavailable || resp.Header.Get(fiber.HeaderRetryAfter) != "30" {
					t.Fatalf("%s: status %d, retry-after %q: %s", tc.ua, resp.StatusCode, resp.Header.Get(fiber.HeaderRetryAfter), body)
				}
				return
			}
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("%s: status %d: %s", tc.ua, resp.StatusCode, body)
			}
			if !strings.HasPrefix(resp.Header.Get(fiber.HeaderContentType), tc.contentType) {
				t.Errorf("Content-Type = %q, want %q", resp.Header.Get(fiber.HeaderContentType), tc.contentType)
			}
			if resp.Header.Get("Subscription-Userinfo") == "" && tc.kind != "html" {
				t.Error("missing Subscription-Userinfo header")
			}
			uuids := accountUUIDs()
			if len(uuids) != 1 || (firstUUID != "" && uuids[0] != firstUUID) {
				t.Fatalf("account UUID changed between clients: %v (first %q)", uuids, firstUUID)
			}
			firstUUID = uuids[0]
			switch tc.kind {
			case "clash-meta":
				var config struct {
					Proxies []struct {
						Type   string `yaml:"type"`
						Server string `yaml:"server"`
					} `yaml:"proxies"`
				}
				if err := yaml.Unmarshal(body, &config); err != nil {
					t.Fatal(err)
				}
				if len(config.Proxies) == 0 || config.Proxies[0].Type != "vless" || config.Proxies[0].Server != f.entryHost {
					t.Fatalf("unexpected Clash Meta profile: %+v", config.Proxies)
				}
			case "sing-box":
				var config struct {
					Outbounds []struct {
						Type string `json:"type"`
					} `json:"outbounds"`
				}
				if err := json.Unmarshal(body, &config); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, outbound := range config.Outbounds {
					found = found || outbound.Type == "vless"
				}
				if !found {
					t.Fatalf("sing-box profile has no VLESS outbound: %s", body)
				}
			case "v2ray":
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
				if err != nil {
					t.Fatalf("not base64 share links: %v: %s", err, body)
				}
				links := strings.Fields(string(decoded))
				if len(links) == 0 {
					t.Fatal("no share links")
				}
				link, err := url.Parse(links[0])
				if err != nil || link.Scheme != "vless" || link.Hostname() != f.entryHost {
					t.Fatalf("unexpected share link: %q (%v)", links[0], err)
				}
			case "html":
				if !strings.Contains(string(body), "<!doctype html>") || strings.Contains(string(body), "vless://") {
					t.Fatalf("browser did not receive summary page: %s", body)
				}
			}
		})
	}
}
