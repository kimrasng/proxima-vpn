package handlers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Subscription output formats. The account URL picks one from the requesting
// client, so users import a single URL into any app.
const (
	formatClashMeta  = "clash-meta"
	formatClash      = "clash" // legacy Clash/Stash: no VLESS, Hysteria2 or SS-2022
	formatSingbox    = "sing-box"
	formatV2ray      = "v2ray" // base64 share links, the universal fallback
	formatSurfboard  = "surfboard"
	formatQuantumult = "quantumult"
	formatWireGuard  = "wireguard"
	formatHTML       = "html"
)

// pathClientTypes are the explicit overrides served at /sub/{token}/{client_type}
// for apps whose User-Agent is not recognized.
var pathClientTypes = map[string]string{
	"clash-meta": formatClashMeta,
	"clash":      formatClash,
	"sing-box":   formatSingbox,
	"v2ray":      formatV2ray,
	"surfboard":  formatSurfboard,
	"quantumult": formatQuantumult,
	"wireguard":  formatWireGuard,
}

// queryFormats keeps ?format= URLs saved before User-Agent detection working.
// "clash" has always produced the Meta profile there, so it still does.
var queryFormats = map[string]string{
	"clash":      formatClashMeta,
	"clash-meta": formatClashMeta,
	"singbox":    formatSingbox,
	"sing-box":   formatSingbox,
	"v2ray":      formatV2ray,
	"surfboard":  formatSurfboard,
	"quantumult": formatQuantumult,
	"wireguard":  formatWireGuard,
}

type userAgentRule struct {
	pattern *regexp.Regexp
	format  string
}

// userAgentRules are evaluated in order. Meta-based Clash apps must be matched
// before the generic Clash pattern: only the Meta dialect carries VLESS, so a
// Meta app served the legacy profile would lose most servers.
var userAgentRules = []userAgentRule{
	{regexp.MustCompile(`(?i)^(clash-verge|clash[-. ]?meta|clashx[-. ]?meta|flclash|mihomo|clash-nyanpasu|koala-?clash|clashmi)`), formatClashMeta},
	{regexp.MustCompile(`(?i)^(clash|stash)`), formatClash},
	{regexp.MustCompile(`(?i)^(sfa|sfi|sfm|sft|sing-?box|karing|hiddify)`), formatSingbox},
	{regexp.MustCompile(`(?i)^surfboard`), formatSurfboard},
	{regexp.MustCompile(`(?i)^quantumult`), formatQuantumult},
}

// Only a browser navigation should receive the human-readable page. Some
// subscription clients request text/html (or */*) while importing a URL, so
// Accept alone cannot distinguish them from a browser. Unknown clients get
// the universal base64 share-link format instead.
var browserUserAgent = regexp.MustCompile(`(?i)^(mozilla/|opera/|opr/)`)

// detectClientFormat maps a known client User-Agent to its native format.
// Unrecognized apps (v2rayN/NG, Happ, Streisand, Shadowrocket, NekoBox, ...)
// receive base64 links, even if they happen to send Accept: text/html.
func detectClientFormat(userAgent, accept string) string {
	ua := strings.TrimSpace(userAgent)
	for _, rule := range userAgentRules {
		if rule.pattern.MatchString(ua) {
			return rule.format
		}
	}
	if browserUserAgent.MatchString(ua) && strings.Contains(strings.ToLower(accept), "text/html") {
		return formatHTML
	}
	return formatV2ray
}

// resolveSubscriptionFormat applies, in priority order: an explicit path
// client type, a legacy ?format= value, then client detection.
func resolveSubscriptionFormat(c *fiber.Ctx, override string) string {
	if override != "" {
		return override
	}
	if format, ok := queryFormats[strings.ToLower(c.Query("format"))]; ok {
		return format
	}
	return detectClientFormat(c.Get(fiber.HeaderUserAgent), c.Get(fiber.HeaderAccept))
}

type subscriptionSettings struct {
	updateIntervalSeconds int
	profileTitle          string
	supportURL            string
}

// setSubscriptionHeaders emits the de-facto subscription metadata headers read
// by Clash Verge, mihomo, sing-box apps, Hiddify, v2rayN and Happ.
func setSubscriptionHeaders(c *fiber.Ctx, user subscriptionUser, s subscriptionSettings) {
	var total, expire int64
	if user.TrafficLimit != nil {
		total = *user.TrafficLimit
	}
	if user.PlanExpiresAt != nil {
		expire = user.PlanExpiresAt.Unix()
	}
	c.Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d", user.TrafficUsed, total, expire))
	// Clients read this header in hours; the setting is stored in seconds.
	hours := (s.updateIntervalSeconds + 3599) / 3600
	if hours < 1 {
		hours = 1
	}
	c.Set("Profile-Update-Interval", fmt.Sprintf("%d", hours))
	c.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(s.profileTitle)))
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		asciiFilename(s.profileTitle), url.PathEscape(s.profileTitle)))
	if s.supportURL != "" {
		c.Set("Support-Url", s.supportURL)
	}
}

// asciiFilename is the quoted-filename fallback for clients that ignore
// filename*; it must not carry quotes, separators or non-ASCII bytes.
func asciiFilename(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-_.")
	if name == "" {
		return "proxima"
	}
	return name
}

var subscriptionPageText = map[string]map[string]string{
	"ko": {"title": "구독 정보", "status": "상태", "active": "사용 중", "traffic": "사용량", "unlimited": "무제한", "expires": "만료일", "never": "없음", "hint": "이 페이지 주소를 VPN 앱의 구독 추가에 붙여넣으면 앱에 맞는 설정이 자동으로 적용됩니다."},
	"en": {"title": "Subscription", "status": "Status", "active": "Active", "traffic": "Usage", "unlimited": "Unlimited", "expires": "Expires", "never": "Never", "hint": "Paste this page's address into your VPN app's \"add subscription\"; the app receives the matching configuration automatically."},
	"zh": {"title": "订阅信息", "status": "状态", "active": "使用中", "traffic": "用量", "unlimited": "无限制", "expires": "到期", "never": "无", "hint": "将此页面地址粘贴到 VPN 应用的“添加订阅”中，应用会自动获得匹配的配置。"},
}

var subscriptionPage = template.Must(template.New("sub").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.T.title}}</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:2rem auto;padding:0 1rem;color:#16191f}dl{display:grid;grid-template-columns:auto 1fr;gap:.5rem 1rem}dt{color:#5f6b7a}</style>
</head><body><h1>{{.T.title}}</h1>
<dl><dt>{{.T.status}}</dt><dd>{{.T.active}}</dd><dt>{{.T.traffic}}</dt><dd>{{.Traffic}}</dd><dt>{{.T.expires}}</dt><dd>{{.Expires}}</dd></dl>
<p>{{.T.hint}}</p></body></html>`))

// renderSubscriptionPage answers a browser opening the subscription URL with a
// readable summary instead of a config blob. Only eligible accounts reach it.
func renderSubscriptionPage(c *fiber.Ctx, user subscriptionUser) error {
	lang := user.Language
	text, ok := subscriptionPageText[lang]
	if !ok {
		lang, text = "en", subscriptionPageText["en"]
	}
	traffic := text["unlimited"]
	if user.TrafficLimit != nil && *user.TrafficLimit > 0 {
		traffic = fmt.Sprintf("%.1f / %.1f GB", float64(user.TrafficUsed)/(1<<30), float64(*user.TrafficLimit)/(1<<30))
	}
	expires := text["never"]
	if user.PlanExpiresAt != nil {
		expires = user.PlanExpiresAt.Format(time.DateOnly)
	}
	var out bytes.Buffer
	if err := subscriptionPage.Execute(&out, map[string]any{"Lang": lang, "T": text, "Traffic": traffic, "Expires": expires}); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to render subscription page"})
	}
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.Send(out.Bytes())
}

func noReadyServers(c *fiber.Ctx) error {
	c.Set(fiber.HeaderRetryAfter, "30")
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "no servers are ready for this subscription yet; retry shortly"})
}
