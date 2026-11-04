package ruleset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
)

type mobileTransport func(*http.Request) (*http.Response, error)

func (f mobileTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestMobileProvidersPreserveOrderAndResolvePayloads(t *testing.T) {
	base := subscription.MetaRulesBase
	profile := subscription.RoutingProfile{
		Providers: []subscription.RoutingProvider{
			{Key: "site", Type: "http", Behavior: "domain", Format: "mrs", URL: base + "/geosite/naver.mrs"},
			{Key: "ip", Type: "http", Behavior: "ipcidr", Format: "mrs", URL: base + "/geoip/kr.mrs"},
			{Key: "yaml", Type: "http", Behavior: "classical", Format: "yaml", URL: "https://raw.githubusercontent.com/example/rules/main/a.yaml"},
			{Key: "text", Type: "http", Behavior: "domain", Format: "text", URL: "https://cdn.jsdelivr.net/example.list"},
			{Key: "inline", Type: "inline", Behavior: "domain", Payload: []string{"+.example.org"}},
		},
		Rules: []subscription.RoutingRule{
			{Type: "DOMAIN", Value: "first.org", Target: "DIRECT"},
			{Type: "RULE-SET", Value: "site", Target: "DIRECT"},
			{Type: "RULE-SET", Value: "inline", Target: "REJECT"},
		},
	}
	fetcher := NewFetcher()
	fetcher.client.Transport = mobileTransport(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.String() {
		case base + "/geosite/naver.list":
			body = "# comment\n+.naver.com\nnaver.net\n"
		case base + "/geoip/kr.list":
			body = "1.2.0.0/16\n2001:db8::/32\n"
		case profile.Providers[2].URL:
			body = "payload:\n  - DOMAIN-SUFFIX,example.com\n  - IP-CIDR,10.0.0.0/8,no-resolve\n"
		case profile.Providers[3].URL:
			body = "example.net\n+.example.com\n"
		default:
			t.Fatalf("unexpected URL %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})

	got, payloads, err := resolveMobileProviders(context.Background(), profile, fetcher.Fetch)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"site": {"+.naver.com", "naver.net"}, "ip": {"1.2.0.0/16", "2001:db8::/32"},
		"yaml": {"DOMAIN-SUFFIX,example.com", "IP-CIDR,10.0.0.0/8,no-resolve"},
		"text": {"example.net", "+.example.com"}, "inline": {"+.example.org"},
	}
	if !reflect.DeepEqual(payloads, want) || !reflect.DeepEqual(got.Rules, profile.Rules) {
		t.Fatalf("payloads=%v rules=%v", payloads, got.Rules)
	}
	for i, p := range got.Providers {
		if p.Key != profile.Providers[i].Key {
			t.Fatal("provider order changed")
		}
		if i < 2 && (p.Format != "text" || p.URL != strings.TrimSuffix(profile.Providers[i].URL, ".mrs")+".list") {
			t.Fatalf("converted provider=%+v", p)
		}
		if i >= 2 && !reflect.DeepEqual(p, profile.Providers[i]) {
			t.Fatalf("provider unexpectedly changed: %+v", p)
		}
	}
	got.Providers[4].Payload[0] = "changed.org"
	payloads["inline"][0] = "also-changed.org"
	if profile.Providers[4].Payload[0] != "+.example.org" || profile.Providers[0].Format != "mrs" {
		t.Fatal("input profile mutated")
	}
}

func TestMobileNoncanonicalMRSBecomesInline(t *testing.T) {
	data, err := os.ReadFile("testdata/geosite-kakao.mrs")
	if err != nil {
		t.Fatal(err)
	}
	fetcher := NewFetcher()
	fetcher.client.Transport = mobileTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})
	p := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "kakao", Type: "http", Behavior: "domain", Format: "mrs", URL: "https://fastly.jsdelivr.net/rules.mrs", Interval: 86400}}}
	got, payloads, err := resolveMobileProviders(context.Background(), p, fetcher.Fetch)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeDomains(readList(t, "testdata/geosite-kakao.list"))
	if !reflect.DeepEqual(normalizeDomains(payloads["kakao"]), want) {
		t.Fatalf("MRS payload does not match list")
	}
	provider := got.Providers[0]
	if provider.Type != "inline" || provider.URL != "" || provider.Format != "" || provider.Interval != 0 || !reflect.DeepEqual(provider.Payload, payloads["kakao"]) {
		t.Fatalf("inline provider=%+v", provider)
	}
}

func TestMobileProvidersRejectUnsafeOrIncompleteContents(t *testing.T) {
	for _, tc := range []struct {
		name, behavior string
		contents       Contents
	}{
		{"truncated", "domain", Contents{Entries: []string{"a.org"}, Count: 1, Truncated: true}},
		{"count mismatch", "domain", Contents{Entries: []string{"a.org"}, Count: 2}},
		{"behavior mismatch", "domain", Contents{Behavior: "ipcidr", Entries: []string{"a.org"}, Count: 1}},
		{"empty", "domain", Contents{}},
		{"domain injection", "domain", Contents{Entries: []string{"a.org,DIRECT"}, Count: 1}},
		{"wildcard domain", "domain", Contents{Entries: []string{"*.example"}, Count: 1}},
		{"newline injection", "domain", Contents{Entries: []string{"a.org\nMATCH,REJECT"}, Count: 1}},
		{"invalid prefix", "ipcidr", Contents{Entries: []string{"a.org"}, Count: 1}},
		{"empty classical target", "classical", Contents{Entries: []string{"DOMAIN,a.org,"}, Count: 1}},
		{"unsupported classical", "classical", Contents{Entries: []string{"PROCESS-NAME,app"}, Count: 1}},
		{"wrong IP family", "classical", Contents{Entries: []string{"IP-CIDR6,10.0.0.0/8"}, Count: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "bad", Type: "http", Format: "text", Behavior: tc.behavior, URL: "https://cdn.jsdelivr.net/rules.list"}}}
			got, payloads, err := resolveMobileProviders(context.Background(), p, func(context.Context, string, string) (Contents, error) { return tc.contents, nil })
			if err == nil || !strings.Contains(err.Error(), "bad") || payloads != nil || len(got.Providers) != 0 {
				t.Fatalf("partial/accepted result: %+v %v %v", got, payloads, err)
			}
		})
	}
}

func TestMobileClassicalTargetsArePreserved(t *testing.T) {
	profile := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "rules", Type: "inline", Behavior: "classical", Payload: []string{"DOMAIN-SUFFIX,example.com,Japan Proxy", "IP-CIDR,192.0.2.0/24,DIRECT,no-resolve"}}}}
	got, payloads, err := resolveMobileProviders(context.Background(), profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Providers[0].Payload, profile.Providers[0].Payload) || !reflect.DeepEqual(payloads["rules"], profile.Providers[0].Payload) {
		t.Fatalf("targets not preserved: %+v %v", got, payloads)
	}
}

func TestMobileProvidersRejectDisallowedURLs(t *testing.T) {
	for _, rawURL := range []string{"http://cdn.jsdelivr.net/a.list", "https://127.0.0.1/a.list", "https://user@cdn.jsdelivr.net/a.list", "https://cdn.jsdelivr.net:8443/a.list"} {
		t.Run(rawURL, func(t *testing.T) {
			p := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "unsafe", Type: "http", Format: "text", Behavior: "domain", URL: rawURL}}}
			_, _, err := ResolveMobileProviders(context.Background(), p)
			if err == nil {
				t.Fatalf("expected host rejection, got %v", err)
			}
		})
	}
}

func TestMobileProvidersBoundAggregatePayload(t *testing.T) {
	payload := make([]string, maxEntries)
	for i := range payload {
		payload[i] = strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + ".org"
	}
	p := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "large", Type: "inline", Behavior: "domain", Payload: payload}}}
	if _, _, err := ResolveMobileProviders(context.Background(), p); err == nil {
		t.Fatal("oversized aggregate accepted")
	}
}

func TestMobileProvidersRejectCanonicalBehaviorMismatch(t *testing.T) {
	p := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "wrong", Type: "http", Behavior: "domain", Format: "mrs", URL: subscription.MetaRulesBase + "/geoip/kr.mrs"}}}
	_, _, err := resolveMobileProviders(context.Background(), p, func(context.Context, string, string) (Contents, error) {
		t.Fatal("behavior mismatch must fail before fetch")
		return Contents{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "requires ipcidr") {
		t.Fatalf("expected canonical behavior mismatch, got %v", err)
	}
}

func TestMobileProvidersPreserveFetchAndCancellationErrors(t *testing.T) {
	p := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "network", Type: "http", Behavior: "domain", Format: "text", URL: "https://cdn.jsdelivr.net/rules.list"}}}
	downloadErr := errors.New("download failed")
	_, _, err := resolveMobileProviders(context.Background(), p, func(context.Context, string, string) (Contents, error) {
		return Contents{}, downloadErr
	})
	if !errors.Is(err, downloadErr) {
		t.Fatalf("download cause lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = ResolveMobileProviders(ctx, p)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
}

func TestMobileRemoteLimitCountsRawBytesIncludingComments(t *testing.T) {
	for _, size := range []int{maxMobileRemoteBytes, maxMobileRemoteBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			body := "example.com\n#" + strings.Repeat("x", size-len("example.com\n#"))
			fetcher := NewFetcher()
			fetcher.client.Transport = mobileTransportLimit{base: mobileTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			p := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "limited", Type: "http", Behavior: "domain", Format: "text", URL: "https://cdn.jsdelivr.net/example.list"}}}
			got, payloads, err := resolveMobileProviders(context.Background(), p, fetcher.Fetch)
			if size > maxMobileRemoteBytes {
				if err == nil || !strings.Contains(err.Error(), "iOS limit") || payloads != nil || len(got.Providers) != 0 {
					t.Fatalf("oversized remote not rejected: %+v %v %v", got, payloads, err)
				}
			} else if err != nil || !reflect.DeepEqual(payloads["limited"], []string{"example.com"}) || got.Providers[0].Type != "http" {
				t.Fatalf("limit boundary failed: %+v %v %v", got, payloads, err)
			}
		})
	}
}
