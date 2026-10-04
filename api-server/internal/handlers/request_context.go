package handlers

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/useragent"
)

// checkoutContext is the bounded, non-gating request context stored alongside a
// plan order. Nothing here is ever read back as an authorization, eligibility,
// rate-limit or uniqueness input - it exists so an administrator reviewing a
// disputed charge can see where the order came from.
type checkoutContext struct {
	OriginHost        string
	ClientIP          string
	UserAgent         string
	BrowserFamily     string
	OSFamily          string
	Locale            string
	DeviceFingerprint string
}

const (
	maxDeviceFingerprintBytes = 128
	maxLocaleBytes            = 35
)

// captureCheckoutContext derives the audit context of the current request.
// Every field is best-effort: a missing header yields an empty string rather
// than an error, because Referrer-Policy already makes Referer unreliable and a
// blank audit field must never fail a paying customer's order.
func captureCheckoutContext(c *fiber.Ctx, deviceFingerprint string) checkoutContext {
	rawAgent := c.Get(fiber.HeaderUserAgent)
	classified := useragent.Classify(rawAgent)

	return checkoutContext{
		OriginHost:        originHost(c),
		ClientIP:          c.IP(),
		UserAgent:         services.TruncateUTF8(rawAgent, services.MaxUserAgentBytes),
		BrowserFamily:     string(classified.Browser),
		OSFamily:          string(classified.OS),
		Locale:            primaryLocaleTag(c.Get(fiber.HeaderAcceptLanguage)),
		DeviceFingerprint: services.TruncateUTF8(deviceFingerprint, maxDeviceFingerprintBytes),
	}
}

// originHost returns the host the checkout was initiated from, preferring
// Origin and falling back to Referer. Only the host is kept: a Referer path can
// carry a promotion code, a reset token or any other secret the user's browser
// happened to have in the URL, and none of that belongs in an audit column.
func originHost(c *fiber.Ctx) string {
	for _, header := range []string{fiber.HeaderOrigin, fiber.HeaderReferer} {
		if host := hostOf(c.Get(header)); host != "" {
			return host
		}
	}
	return ""
}

func hostOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Host
}

// primaryLocaleTag extracts the first language tag of an Accept-Language
// header and normalizes it to the canonical lower-language/upper-region form.
// Anything that is not a plain tag - "*", injected text, an overlong value - is
// dropped rather than stored, keeping the column to a display hint.
func primaryLocaleTag(header string) string {
	primary, _, _ := strings.Cut(header, ",")
	primary, _, _ = strings.Cut(primary, ";")
	primary = strings.TrimSpace(primary)
	if primary == "" || len(primary) > maxLocaleBytes {
		return ""
	}

	language, region, hasRegion := strings.Cut(primary, "-")
	if !isAlpha(language) {
		return ""
	}
	if !hasRegion {
		return strings.ToLower(language)
	}
	if !isAlphanumeric(region) {
		return ""
	}
	return strings.ToLower(language) + "-" + strings.ToUpper(region)
}

func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func isAlphanumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
