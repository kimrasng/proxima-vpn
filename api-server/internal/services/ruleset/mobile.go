package ruleset

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
)

// Bound the entire resolved profile, not just each individual download.
const maxMobilePayloadBytes = 8 << 20
const maxMobilePayloadEntries = 100000
const maxMobileRemoteBytes = 2 << 20

// Keep Fetcher's URL and redirect restrictions while imposing the mobile
// client's smaller limit on raw response bytes (including comments/YAML).
type mobileTransportLimit struct{ base http.RoundTripper }

func (t mobileTransportLimit) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	res.Body = &mobileBodyLimit{ReadCloser: res.Body, remaining: maxMobileRemoteBytes}
	return res, nil
}

type mobileBodyLimit struct {
	io.ReadCloser
	remaining int
}

func (b *mobileBodyLimit) Read(p []byte) (int, error) {
	p = p[:min(len(p), b.remaining+1)]
	n, err := b.ReadCloser.Read(p)
	if n > b.remaining {
		return 0, fmt.Errorf("remote provider exceeds iOS limit of %d bytes", maxMobileRemoteBytes)
	}
	b.remaining -= n
	return n, err
}

var canonicalMobileMRS = regexp.MustCompile(`^https://cdn\.jsdelivr\.net/gh/MetaCubeX/meta-rules-dat@meta/geo/(geosite|geoip)/([A-Za-z0-9_@.-]+)\.mrs$`)

// ResolveMobileProviders returns an iOS-compatible profile and Android payloads
// keyed by provider key. HTTP text/yaml URLs remain HTTP in the returned profile;
// canonical MetaCubeX MRS URLs become their text .list twins. Other MRS files
// are decoded into inline providers. Every provider is resolved for Android,
// including inline providers. Provider, rule and payload order are preserved.
// The input is not mutated. Any incomplete or unsupported payload fails the
// entire operation; callers must not render a partial result on error.
func ResolveMobileProviders(ctx context.Context, profile subscription.RoutingProfile) (subscription.RoutingProfile, map[string][]string, error) {
	return resolveMobileProviders(ctx, profile, cachedMobileProvider)
}

func resolveMobileProviders(ctx context.Context, profile subscription.RoutingProfile, fetch func(context.Context, string, string) (Contents, error)) (subscription.RoutingProfile, map[string][]string, error) {
	out := profile
	out.Providers = slices.Clone(profile.Providers)
	payloads := make(map[string][]string, len(profile.Providers))
	bytes, entries := 0, 0
	for i, provider := range profile.Providers {
		if err := ctx.Err(); err != nil {
			return subscription.RoutingProfile{}, nil, err
		}
		fail := func(err error) (subscription.RoutingProfile, map[string][]string, error) {
			return subscription.RoutingProfile{}, nil, fmt.Errorf("mobile provider %q: %w", provider.Key, err)
		}
		if _, exists := payloads[provider.Key]; exists || provider.Key == "" {
			return fail(fmt.Errorf("empty or duplicate key"))
		}
		if provider.Behavior != "domain" && provider.Behavior != "ipcidr" && provider.Behavior != "classical" {
			return fail(fmt.Errorf("unsupported behavior %q", provider.Behavior))
		}
		var payload []string
		switch provider.Type {
		case "inline":
			payload = provider.Payload
		case "http":
			u, err := mobileHTTPSURL(provider.URL)
			if err != nil {
				return fail(err)
			}
			format := provider.Format
			if format == "" {
				format = formatFromPath(u.Path)
			}
			if format != "mrs" && format != "text" && format != "yaml" {
				return fail(fmt.Errorf("unsupported format %q", format))
			}
			if match := canonicalMobileMRS.FindStringSubmatch(provider.URL); format == "mrs" && match != nil {
				behavior := "domain"
				if match[1] == "geoip" {
					behavior = "ipcidr"
				}
				if provider.Behavior != behavior {
					return fail(fmt.Errorf("canonical %s URL requires %s behavior, got %s", match[1], behavior, provider.Behavior))
				}
				provider.URL = strings.TrimSuffix(provider.URL, ".mrs") + ".list"
				format = "text"
			}
			contents, err := fetch(ctx, provider.URL, format)
			if err != nil {
				return fail(fmt.Errorf("fetch: %w", err))
			}
			if contents.Truncated || contents.Count != len(contents.Entries) {
				return fail(fmt.Errorf("incomplete payload: count=%d entries=%d truncated=%t", contents.Count, len(contents.Entries), contents.Truncated))
			}
			if contents.Behavior != "" && contents.Behavior != provider.Behavior {
				return fail(fmt.Errorf("decoded behavior %q differs from declared %q", contents.Behavior, provider.Behavior))
			}
			payload = contents.Entries
			provider.Format = format
			if format == "mrs" {
				provider.Type, provider.Format, provider.URL, provider.Interval = "inline", "", "", 0
				provider.Payload = slices.Clone(payload)
			}
		default:
			return fail(fmt.Errorf("unsupported type %q", provider.Type))
		}
		if len(payload) == 0 {
			return fail(fmt.Errorf("empty payload"))
		}
		for j, entry := range payload {
			if err := mobileEntryProblem(provider.Behavior, entry); err != nil {
				return fail(fmt.Errorf("entry %d: %w", j+1, err))
			}
			bytes += len(entry)
		}
		entries += len(payload)
		if bytes > maxMobilePayloadBytes || entries > maxMobilePayloadEntries {
			return fail(fmt.Errorf("resolved payload exceeds %d bytes or %d entries", maxMobilePayloadBytes, maxMobilePayloadEntries))
		}
		provider.Payload = slices.Clone(provider.Payload)
		out.Providers[i] = provider
		payloads[provider.Key] = slices.Clone(payload)
	}
	return out, payloads, nil
}

// Accept only rule forms representable by both mobile generators.
func mobileEntryProblem(behavior, entry string) error {
	if entry == "" || len(entry) > 512 || strings.IndexFunc(entry, unicode.IsControl) >= 0 || (behavior != "classical" && strings.IndexFunc(entry, unicode.IsSpace) >= 0) {
		return fmt.Errorf("entry must be 1-512 characters without controls or invalid whitespace")
	}
	switch behavior {
	case "domain":
		domain := strings.TrimPrefix(entry, "+.")
		if len(domain) == 0 || len(domain) > 253 || strings.ContainsAny(domain, ",/:;#@+*") {
			return fmt.Errorf("unsupported domain %q", entry)
		}
		for _, label := range strings.Split(domain, ".") {
			if label == "" || len(label) > 63 {
				return fmt.Errorf("invalid domain %q", entry)
			}
			for _, c := range label {
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
					return fmt.Errorf("unsupported domain %q", entry)
				}
			}
		}
	case "ipcidr":
		if _, err := netip.ParsePrefix(entry); err != nil {
			return fmt.Errorf("invalid IP prefix %q: %w", entry, err)
		}
	case "classical":
		parts := strings.Split(entry, ",")
		if len(parts) < 2 || len(parts) > 4 {
			return fmt.Errorf("unsupported classical rule %q", entry)
		}
		if len(parts) >= 3 && parts[2] == "" {
			return fmt.Errorf("empty classical target in %q", entry)
		}
		if strings.IndexFunc(parts[0], unicode.IsSpace) >= 0 || strings.IndexFunc(parts[1], unicode.IsSpace) >= 0 || (len(parts) >= 3 && strings.TrimSpace(parts[2]) != parts[2]) {
			return fmt.Errorf("invalid classical whitespace in %q", entry)
		}
		if len(parts) == 4 && parts[3] != "no-resolve" {
			return fmt.Errorf("invalid classical option in %q", entry)
		}
		if (len(parts) == 3 && parts[2] == "no-resolve" || len(parts) == 4) && parts[0] != "IP-CIDR" && parts[0] != "IP-CIDR6" {
			return fmt.Errorf("no-resolve applies only to IP rules: %q", entry)
		}
		switch parts[0] {
		case "DOMAIN", "DOMAIN-SUFFIX":
			return mobileEntryProblem("domain", parts[1])
		case "DOMAIN-KEYWORD":
			if parts[1] == "" || strings.ContainsAny(parts[1], "/:;#@") {
				return fmt.Errorf("invalid domain keyword %q", entry)
			}
		case "IP-CIDR", "IP-CIDR6":
			prefix, err := netip.ParsePrefix(parts[1])
			if err != nil || prefix.Addr().Is4() != (parts[0] == "IP-CIDR") {
				return fmt.Errorf("invalid classical IP prefix %q", entry)
			}
		case "DST-PORT":
			lo, hi, ranged := strings.Cut(parts[1], "-")
			a, err := strconv.Atoi(lo)
			if err != nil || a < 1 || a > 65535 {
				return fmt.Errorf("invalid port %q", entry)
			}
			if ranged {
				b, err := strconv.Atoi(hi)
				if err != nil || b < a || b > 65535 {
					return fmt.Errorf("invalid port range %q", entry)
				}
			}
		default:
			return fmt.Errorf("unsupported classical rule type %q", parts[0])
		}
	}
	return nil
}
