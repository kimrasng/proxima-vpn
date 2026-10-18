package handlers

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestDetectClientFormat(t *testing.T) {
	for _, tc := range []struct {
		ua, accept, want string
	}{
		// Meta-based Clash apps must win over the generic Clash rule.
		{"clash-verge/v2.0.3", "", formatClashMeta},
		{"Clash-Verge/v1.7.7", "", formatClashMeta},
		{"clash.meta", "", formatClashMeta},
		{"ClashMetaForAndroid/2.11.1.Meta", "", formatClashMeta},
		{"ClashX Meta/1.4.0", "", formatClashMeta},
		{"FlClash/v0.8.70 clash-verge", "", formatClashMeta},
		{"mihomo/1.18.0", "", formatClashMeta},
		{"clash-nyanpasu/v1.6.1", "", formatClashMeta},
		{"Koala-Clash/1.0", "", formatClashMeta},
		{"ClashforWindows/0.20.39", "", formatClash},
		{"Clash/1.18.0", "", formatClash},
		{"Stash/2.4.0 Clash/1.9.0", "", formatClash},
		{"SFA/1.10.0 (Android)", "", formatSingbox},
		{"SFI/1.10.0", "", formatSingbox},
		{"sing-box 1.11.4", "", formatSingbox},
		{"HiddifyNext/2.5.7", "", formatSingbox},
		{"Hiddify/2.0", "", formatSingbox},
		{"Karing/1.1.2", "", formatSingbox},
		{"Surfboard/2.24", "", formatSurfboard},
		{"Quantumult%20X/1.4.1", "", formatQuantumult},
		// Share-link clients, unknown apps and empty UAs get base64 links.
		{"v2rayN/6.42", "", formatV2ray},
		{"v2rayNG/1.9.16", "", formatV2ray},
		{"Happ/1.63.1", "", formatV2ray},
		{"Streisand/1.6", "", formatV2ray},
		{"Shadowrocket/2070", "", formatV2ray},
		{"", "", formatV2ray},
		{"curl/8.4.0", "*/*", formatV2ray},
		{"curl/8.4.0", "text/html", formatV2ray},
		{"unknown-app/1.0", "text/html,application/xhtml+xml", formatV2ray},
		{"", "text/html", formatV2ray},
		// Browsers get the readable page; a recognized client never does.
		{"Mozilla/5.0 (Macintosh)", "text/html,application/xhtml+xml", formatHTML},
		{"Mozilla/5.0 (Macintosh)", "*/*", formatV2ray},
		{"Opera/9.80", "text/html", formatHTML},
		{"clash-verge/v2.0.3", "text/html", formatClashMeta},
	} {
		if got := detectClientFormat(tc.ua, tc.accept); got != tc.want {
			t.Errorf("detectClientFormat(%q, %q) = %q, want %q", tc.ua, tc.accept, got, tc.want)
		}
	}
}

func TestResolveSubscriptionFormatPriority(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString(resolveSubscriptionFormat(c, c.Get("X-Override")))
	})
	for _, tc := range []struct {
		query, ua, override, want string
	}{
		{"", "clash-verge/v2.0.3", "", formatClashMeta},
		{"", "clash-verge/v2.0.3", formatSingbox, formatSingbox}, // path override wins
		{"?format=clash", "v2rayN/6.42", "", formatClashMeta},    // saved legacy URL
		{"?format=singbox", "", "", formatSingbox},               //
		{"?format=bogus", "SFA/1.10.0", "", formatSingbox},       // unknown query ignored
		{"?format=v2ray", "clash-verge/v2.0.3", "", formatV2ray}, //
		{"", "Mozilla/5.0", formatClashMeta, formatClashMeta},    // override beats browser page
		{"", "unknown-app/1.0", formatSurfboard, formatSurfboard},
		{"", "unknown-app/1.0", formatQuantumult, formatQuantumult},
	} {
		req := httptest.NewRequest(fiber.MethodGet, "/"+tc.query, nil)
		req.Header.Set("User-Agent", tc.ua)
		req.Header.Set("Accept", "text/html")
		if tc.override != "" {
			req.Header.Set("X-Override", tc.override)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != tc.want {
			t.Errorf("query=%q ua=%q override=%q: got %q, want %q", tc.query, tc.ua, tc.override, body, tc.want)
		}
	}
}

func TestPathClientTypesCannotShadowDeviceIDs(t *testing.T) {
	// Device IDs are UUIDs; no client type may look like one.
	for name := range pathClientTypes {
		if len(name) == 36 && strings.Count(name, "-") == 4 {
			t.Fatalf("client type %q is UUID-shaped", name)
		}
	}
}

func TestSetSubscriptionHeadersReflectsEachPlan(t *testing.T) {
	plans := []struct {
		name         string
		used, total  int64
		expiry       int64
		wantInterval string
	}{
		{name: "starter-30-days-10GiB", used: 123456, total: 10 << 30, expiry: 1893456000, wantInterval: "24"},
		{name: "pro-60-days-60GiB", used: 5400921375, total: 60 << 30, expiry: 1896048000, wantInterval: "24"},
		{name: "unlimited-no-expiry", used: 7654321, total: 0, expiry: 0, wantInterval: "24"},
	}
	for _, plan := range plans {
		t.Run(plan.name, func(t *testing.T) {
			var limitPtr *int64
			if plan.total > 0 {
				limitPtr = &plan.total
			}
			user := subscriptionUser{TrafficUsed: plan.used, TrafficLimit: limitPtr}
			if plan.expiry > 0 {
				expires := time.Unix(plan.expiry, 0)
				user.PlanExpiresAt = &expires
			}
			app := fiber.New()
			app.Get("/", func(c *fiber.Ctx) error {
				setSubscriptionHeaders(c, user, subscriptionSettings{updateIntervalSeconds: 86400, profileTitle: "Proxima VPN"})
				return nil
			})
			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			wantInfo := fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d", plan.used, plan.total, plan.expiry)
			if got := resp.Header.Get("Subscription-Userinfo"); got != wantInfo {
				t.Errorf("Subscription-Userinfo = %q, want %q", got, wantInfo)
			}
			if got := resp.Header.Get("Profile-Update-Interval"); got != plan.wantInterval {
				t.Errorf("Profile-Update-Interval = %q, want %q", got, plan.wantInterval)
			}
			if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename=Proxima.yaml; filename*=UTF-8''Proxima.yaml` {
				t.Errorf("Content-Disposition = %q", got)
			}
		})
	}
}

func TestSetSubscriptionHeaders(t *testing.T) {
	limit := int64(60 << 30)
	expires := time.Unix(1893456000, 0)
	user := subscriptionUser{TrafficUsed: 5400921375, TrafficLimit: &limit, PlanExpiresAt: &expires}
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		setSubscriptionHeaders(c, user, subscriptionSettings{updateIntervalSeconds: 24 * 3600, profileTitle: "Proxima VPN", supportURL: "https://example.test/help"})
		return nil
	})
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	h := resp.Header
	if got := h.Get("Subscription-Userinfo"); got != "upload=0; download=5400921375; total=64424509440; expire=1893456000" {
		t.Errorf("Subscription-Userinfo = %q", got)
	}
	if got := h.Get("Profile-Update-Interval"); got != "24" {
		t.Errorf("Profile-Update-Interval = %q, want hours", got)
	}
	title := strings.TrimPrefix(h.Get("Profile-Title"), "base64:")
	if decoded, err := base64.StdEncoding.DecodeString(title); err != nil || string(decoded) != "Proxima VPN" {
		t.Errorf("Profile-Title = %q", h.Get("Profile-Title"))
	}
	if got := h.Get("Content-Disposition"); got != `attachment; filename=Proxima.yaml; filename*=UTF-8''Proxima.yaml` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := h.Get("Support-Url"); got != "https://example.test/help" {
		t.Errorf("Support-Url = %q", got)
	}
	if got := h.Get("Profile-Web-Page-Url"); got != "https://example.test/help" {
		t.Errorf("Profile-Web-Page-Url = %q", got)
	}
}
