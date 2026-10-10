package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// captureFor runs captureCheckoutContext inside a real Fiber handler, which is
// the only way c.IP() and the header accessors behave as they do in production.
func captureFor(t *testing.T, fingerprint string, headers map[string]string) checkoutContext {
	t.Helper()

	var captured checkoutContext
	app := fiber.New()
	app.Get("/capture", func(c *fiber.Ctx) error {
		captured = captureCheckoutContext(c, fingerprint)
		return c.SendStatus(fiber.StatusNoContent)
	})

	request := httptest.NewRequest("GET", "/capture", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("capture request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	return captured
}

func TestCaptureCheckoutContext_StoresOriginHostWithoutPathOrQuery(t *testing.T) {
	got := captureFor(t, "", map[string]string{
		"Origin": "https://panel.example.test:8443/checkout/secret?token=abc",
	})

	if got.OriginHost != "panel.example.test:8443" {
		t.Errorf("OriginHost = %q, want %q", got.OriginHost, "panel.example.test:8443")
	}
	if strings.Contains(got.OriginHost, "/") || strings.Contains(got.OriginHost, "?") {
		t.Errorf("OriginHost = %q leaks a path or query", got.OriginHost)
	}
}

func TestCaptureCheckoutContext_PrefersOriginOverReferer(t *testing.T) {
	got := captureFor(t, "", map[string]string{
		"Origin":  "https://origin.example.test",
		"Referer": "https://referer.example.test/some/page",
	})

	if got.OriginHost != "origin.example.test" {
		t.Errorf("OriginHost = %q, want the Origin host %q", got.OriginHost, "origin.example.test")
	}
}

func TestCaptureCheckoutContext_FallsBackToRefererHostOnly(t *testing.T) {
	got := captureFor(t, "", map[string]string{
		"Referer": "https://referer.example.test/orders/9?promo=SUMMER",
	})

	if got.OriginHost != "referer.example.test" {
		t.Errorf("OriginHost = %q, want the Referer host %q", got.OriginHost, "referer.example.test")
	}
}

func TestCaptureCheckoutContext_MissingOriginAndRefererStaysEmpty(t *testing.T) {
	got := captureFor(t, "", map[string]string{})

	if got.OriginHost != "" {
		t.Errorf("OriginHost = %q, want empty when neither header is present", got.OriginHost)
	}
}

func TestCaptureCheckoutContext_BoundsUserAgentOnRuneBoundary(t *testing.T) {
	// A multi-byte agent longer than the bound: a naive byte slice would cut a
	// rune in half and Postgres would then reject the whole INSERT.
	longUA := strings.Repeat("한", 400)
	got := captureFor(t, "", map[string]string{"User-Agent": longUA})

	if len(got.UserAgent) > services.MaxUserAgentBytes {
		t.Errorf("len(UserAgent) = %d, want <= %d", len(got.UserAgent), services.MaxUserAgentBytes)
	}
	if !utf8.ValidString(got.UserAgent) {
		t.Errorf("UserAgent = %q is not valid UTF-8", got.UserAgent)
	}
	if got.UserAgent == "" {
		t.Error("UserAgent is empty; the bounded prefix should survive")
	}
}

func TestCaptureCheckoutContext_DerivesClosedSetBrowserAndOS(t *testing.T) {
	cases := []struct {
		name            string
		ua              string
		browser, osName string
	}{
		{
			name:    "edge on windows",
			ua:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36 Edg/120.0",
			browser: "edge", osName: "windows",
		},
		{
			name:    "safari on ios",
			ua:      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Safari/604.1",
			browser: "safari", osName: "ios",
		},
		{
			name:    "unrecognized agent",
			ua:      "curl/8.4.0",
			browser: "unknown", osName: "unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := captureFor(t, "", map[string]string{"User-Agent": tc.ua})

			if got.BrowserFamily != tc.browser || got.OSFamily != tc.osName {
				t.Errorf("browser/os = %q/%q, want %q/%q", got.BrowserFamily, got.OSFamily, tc.browser, tc.osName)
			}
			if strings.Contains(got.BrowserFamily, "/") || strings.Contains(got.OSFamily, "/") {
				t.Errorf("browser/os = %q/%q carries raw agent text", got.BrowserFamily, got.OSFamily)
			}
		})
	}
}

func TestCaptureCheckoutContext_NormalizesPrimaryLocaleTag(t *testing.T) {
	cases := []struct {
		name, header, want string
	}{
		{name: "primary tag from a weighted list", header: "ko-KR,ko;q=0.9,en-US;q=0.8", want: "ko-KR"},
		{name: "case normalized", header: "KO-kr", want: "ko-KR"},
		{name: "language only", header: "EN", want: "en"},
		{name: "whitespace trimmed", header: "  fr-CA , fr;q=0.5", want: "fr-CA"},
		{name: "wildcard is not a locale", header: "*", want: ""},
		{name: "junk rejected", header: "<script>alert(1)</script>", want: ""},
		{name: "overlong tag rejected", header: strings.Repeat("a", 64), want: ""},
		{name: "absent header", header: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			if tc.header != "" {
				headers["Accept-Language"] = tc.header
			}
			got := captureFor(t, "", headers)

			if got.Locale != tc.want {
				t.Errorf("Locale = %q, want %q", got.Locale, tc.want)
			}
		})
	}
}

func TestCaptureCheckoutContext_KeepsFingerprintOpaqueAndBounded(t *testing.T) {
	fingerprint := strings.Repeat("한", 200)
	got := captureFor(t, fingerprint, map[string]string{})

	if got.DeviceFingerprint == "" {
		t.Fatal("DeviceFingerprint is empty; the bounded prefix should survive")
	}
	if len(got.DeviceFingerprint) > maxDeviceFingerprintBytes {
		t.Errorf("len(DeviceFingerprint) = %d, want <= %d", len(got.DeviceFingerprint), maxDeviceFingerprintBytes)
	}
	if !utf8.ValidString(got.DeviceFingerprint) {
		t.Errorf("DeviceFingerprint = %q is not valid UTF-8", got.DeviceFingerprint)
	}
	if !strings.HasPrefix(fingerprint, got.DeviceFingerprint) {
		t.Errorf("DeviceFingerprint = %q is not a prefix of the supplied value; it was rewritten rather than stored opaquely", got.DeviceFingerprint)
	}
}

func TestCaptureCheckoutContext_RecordsTrustedClientIP(t *testing.T) {
	got := captureFor(t, "", map[string]string{})

	if got.ClientIP == "" {
		t.Error("ClientIP is empty; c.IP() always resolves to the peer")
	}
}
