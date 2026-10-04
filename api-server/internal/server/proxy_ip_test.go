package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// Fiber's app.Test serves requests over an in-memory connection whose remote
// address is 0.0.0.0, so a fixture listing this address is a trusted peer and
// any fixture that omits it is an untrusted one.
const testPeerIP = "0.0.0.0"

// clientIP builds an app from the given server config and reports what a
// handler's c.IP() sees for a request carrying the given forwarded header.
// An empty header value means the request carries no forwarded header at all.
func clientIP(t *testing.T, cfg *config.Config, header, value string) string {
	t.Helper()

	app := fiber.New(fiberConfig(cfg))

	var seen string
	app.Get("/probe", func(c *fiber.Ctx) error {
		seen = c.IP()
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(fiber.MethodGet, "/probe", nil)
	if value != "" {
		req.Header.Set(header, value)
	}
	if _, err := app.Test(req); err != nil {
		t.Fatalf("probe request: %v", err)
	}
	return seen
}

// proxyConfig returns a server config trusting exactly the given proxies and
// reading the standard forwarded header.
func proxyConfig(trusted ...string) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			ProxyHeader:    "X-Forwarded-For",
			TrustedProxies: trusted,
		},
	}
}

// The whole point of the setting: when the request really did arrive from the
// reverse proxy, the address recorded is the client the proxy is speaking for,
// not the proxy itself.
func TestClientIP_UsesForwardedHeader_whenPeerIsATrustedProxy(t *testing.T) {
	got := clientIP(t, proxyConfig(testPeerIP), "X-Forwarded-For", "203.0.113.9")

	if want := "203.0.113.9"; got != want {
		t.Errorf("c.IP() = %q, want the forwarded client %q", got, want)
	}
}

// Port 2053 is published alongside nginx, so anyone can reach the API directly
// and send whatever X-Forwarded-For they like. That header must be ignored and
// the real peer recorded, or every IP-keyed decision (rate limits, login audit
// rows) is trivially spoofable.
func TestClientIP_IgnoresForwardedHeader_whenPeerIsNotATrustedProxy(t *testing.T) {
	got := clientIP(t, proxyConfig("198.51.100.7"), "X-Forwarded-For", "203.0.113.9")

	if got == "203.0.113.9" {
		t.Fatalf("c.IP() = %q: an untrusted peer's forwarded header was believed", got)
	}
	if got != testPeerIP {
		t.Errorf("c.IP() = %q, want the real peer %q", got, testPeerIP)
	}
}

// A deployment that trusts no proxy at all must fall back to the peer address
// even though the forwarded header is configured.
func TestClientIP_IgnoresForwardedHeader_whenNoProxyIsTrusted(t *testing.T) {
	got := clientIP(t, proxyConfig(), "X-Forwarded-For", "203.0.113.9")

	if got != testPeerIP {
		t.Errorf("c.IP() = %q, want the real peer %q", got, testPeerIP)
	}
}

// X-Forwarded-For accumulates hop by hop and the leftmost entry is whatever the
// original client claimed, so it can be junk. Fiber must skip it and return the
// first entry that actually parses as an address rather than storing the junk.
func TestClientIP_SkipsInvalidLeadingEntries_whenForwardedHeaderIsMalformed(t *testing.T) {
	got := clientIP(t, proxyConfig(testPeerIP), "X-Forwarded-For", "not-an-ip, 203.0.113.9")

	if want := "203.0.113.9"; got != want {
		t.Errorf("c.IP() = %q, want the first valid entry %q", got, want)
	}
}

// A forwarded header consisting entirely of junk leaves nothing to trust, so
// the peer address is the only honest answer.
func TestClientIP_FallsBackToPeer_whenForwardedHeaderHasNoValidEntry(t *testing.T) {
	got := clientIP(t, proxyConfig(testPeerIP), "X-Forwarded-For", "bogus")

	if got != testPeerIP {
		t.Errorf("c.IP() = %q, want the real peer %q", got, testPeerIP)
	}
}

// A non-default header name must be honoured, and only that header - a proxy
// configured to publish X-Real-IP means X-Forwarded-For is unverified input.
func TestClientIP_ReadsConfiguredHeaderOnly_whenProxyHeaderIsCustomised(t *testing.T) {
	cfg := proxyConfig(testPeerIP)
	cfg.Server.ProxyHeader = "X-Real-IP"

	if got, want := clientIP(t, cfg, "X-Real-IP", "203.0.113.9"), "203.0.113.9"; got != want {
		t.Errorf("c.IP() = %q, want %q from the configured header", got, want)
	}
	if got := clientIP(t, cfg, "X-Forwarded-For", "203.0.113.9"); got != testPeerIP {
		t.Errorf("c.IP() = %q, want the real peer %q: an unconfigured header was read", got, testPeerIP)
	}
}

// The trusted-proxy check is what makes the forwarded header safe to read, so
// it is not a configurable choice - it is on whatever the deployment sets.
// Validation is likewise always on: without it Fiber returns the raw header
// value, junk and all.
func TestFiberConfig_AlwaysGuardsTheForwardedHeader(t *testing.T) {
	cfg := proxyConfig("10.0.0.0/8")

	fc := fiberConfig(cfg)

	if !fc.EnableTrustedProxyCheck {
		t.Error("EnableTrustedProxyCheck = false: the forwarded header would be read from any peer")
	}
	if !fc.EnableIPValidation {
		t.Error("EnableIPValidation = false: an unparseable header value would be stored verbatim")
	}
	if got, want := fc.ProxyHeader, cfg.Server.ProxyHeader; got != want {
		t.Errorf("ProxyHeader = %q, want %q", got, want)
	}
	if len(fc.TrustedProxies) != 1 || fc.TrustedProxies[0] != "10.0.0.0/8" {
		t.Errorf("TrustedProxies = %v, want [10.0.0.0/8]", fc.TrustedProxies)
	}
	if want := 10 * 1024 * 1024; fc.BodyLimit != want {
		t.Errorf("BodyLimit = %d, want %d", fc.BodyLimit, want)
	}
}
