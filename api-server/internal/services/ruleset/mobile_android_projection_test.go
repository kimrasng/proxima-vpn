package ruleset

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
)

func TestAndroidProvidersOmitProcessesAndPreserveRemainingRules(t *testing.T) {
	for _, providerType := range []string{"inline", "http"} {
		t.Run(providerType, func(t *testing.T) {
			mixed := []string{"PROCESS-NAME,app.exe", "DOMAIN-SUFFIX,example.com,Japan Proxy", "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT"}
			profile := subscription.RoutingProfile{
				Final: "DIRECT", BlockQUIC: true,
				Providers: []subscription.RoutingProvider{
					{Key: "process", Type: providerType, Behavior: "classical", Payload: []string{"PROCESS-NAME,other.exe,DIRECT"}},
					{Key: "mixed", Type: providerType, Behavior: "classical", Payload: mixed},
					{Key: "domain", Type: "inline", Behavior: "domain", Payload: []string{"+.example.org"}},
				},
				Rules: []subscription.RoutingRule{
					{Type: "DOMAIN", Value: "first.org", Target: "DIRECT"},
					{Type: "RULE-SET", Value: "process", Target: "REJECT"},
					{Type: "PROCESS-NAME", Value: "app.exe", Target: "DIRECT"},
					{Type: "RULE-SET", Value: "mixed", Target: "DIRECT"},
					{Type: "RULE-SET", Value: "domain", Target: "REJECT", NoResolve: true},
				},
			}
			for i := 0; i < 2; i++ {
				if providerType == "http" {
					profile.Providers[i].URL = "https://cdn.jsdelivr.net/" + profile.Providers[i].Key + ".list"
					profile.Providers[i].Format = "text"
				}
			}
			fetch := func(_ context.Context, url, _ string) (Contents, error) {
				payload := mixed
				if url == profile.Providers[0].URL {
					payload = []string{"PROCESS-NAME,other.exe,DIRECT"}
				}
				return Contents{Entries: payload, Count: len(payload)}, nil
			}

			got, payloads, err := resolveMobileProfile(context.Background(), profile, fetch, true)
			if err != nil {
				t.Fatal(err)
			}
			wantRules := []subscription.RoutingRule{profile.Rules[0], profile.Rules[3], profile.Rules[4]}
			wantPayloads := map[string][]string{"mixed": mixed[1:], "domain": {"+.example.org"}}
			if !reflect.DeepEqual(got.Rules, wantRules) || !reflect.DeepEqual(payloads, wantPayloads) || len(got.Providers) != 2 || got.Providers[0].Key != "mixed" || got.Providers[1].Key != "domain" || got.Final != "DIRECT" || !got.BlockQUIC {
				t.Fatalf("incorrect projection: %+v %v", got, payloads)
			}
			if providerType == "inline" && !reflect.DeepEqual(got.Providers[0].Payload, mixed[1:]) {
				t.Fatalf("inline payload not projected: %v", got.Providers[0].Payload)
			}
			got.Rules[0].Value = "changed.org"
			payloads["mixed"][0] = "DOMAIN,changed.org"
			if profile.Rules[0].Value != "first.org" || mixed[1] != "DOMAIN-SUFFIX,example.com,Japan Proxy" || len(profile.Providers) != 3 {
				t.Fatal("input was mutated")
			}
		})
	}
}

func TestAndroidProvidersRejectEmptyAndMalformedProcessPayloads(t *testing.T) {
	for _, providerType := range []string{"inline", "http"} {
		for _, entry := range []string{"", "PROCESS-NAME", "PROCESS-NAME,", "PROCESS-NAME,app.exe,", "PROCESS-NAME,app.exe,DIRECT,extra", "PROCESS-NAME,app.exe,no-resolve", "PROCESS-NAME,app.exe\n", " PROCESS-NAME,app.exe", "PROCESS-NAME," + strings.Repeat("x", 512)} {
			t.Run(providerType+"/"+entry, func(t *testing.T) {
				var payload []string
				if entry != "" {
					payload = []string{entry}
				}
				profile := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "bad", Type: providerType, Behavior: "classical", Format: "text", URL: "https://cdn.jsdelivr.net/bad.list", Payload: payload}}}
				got, payloads, err := resolveMobileProfile(context.Background(), profile, func(context.Context, string, string) (Contents, error) {
					return Contents{Entries: payload, Count: len(payload)}, nil
				}, true)
				if err == nil || !strings.Contains(err.Error(), "bad") || payloads != nil || !reflect.DeepEqual(got, subscription.RoutingProfile{}) {
					t.Fatalf("invalid payload returned data: %+v %v %v", got, payloads, err)
				}
			})
		}
	}
}

func TestAndroidClassicalValidation(t *testing.T) {
	for _, tc := range []struct {
		entry string
		valid bool
	}{
		{"AND,((NETWORK,UDP),(DST-PORT,443))", true},
		{"AND,((NETWORK,UDP),(DST-PORT,443),(DOMAIN-SUFFIX,example.com)),REJECT", true},
		{"NETWORK,UDP", true},
		{"NETWORK,TCP,DIRECT", true},
		{"DST-PORT,8000-8100", true},
		{"IP-CIDR,192.0.2.0/24,DIRECT,no-resolve", true},
		{"AND,((NETWORK,UDP),(DST-PORT,65536))", false},
		{"AND,((NETWORK,QUIC),(DST-PORT,443))", false},
		{"AND,((DOMAIN,example.com),(DOMAIN-SUFFIX,example.org))", false},
		{"AND,((NETWORK,UDP))", false},
		{"AND,((NETWORK,UDP),(DST-PORT,443)),", false},
		{"AND,((NETWORK,UDP),(DST-PORT,443)),REJECT,extra", false},
		{"AND,((NETWORK,UDP),(PROCESS-NAME,app.exe))", false},
		{"AND,((NETWORK,UDP),(DOMAIN,bad/domain))", false},
		{"OR,((NETWORK,UDP),(DST-PORT,443))", false},
		{"NETWORK,UDP,no-resolve", false},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			profile := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "rules", Type: "inline", Behavior: "classical", Payload: []string{tc.entry}}}}
			got, payloads, err := ResolveAndroidProviders(context.Background(), profile)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
			if !tc.valid && (payloads != nil || len(got.Providers) != 0) {
				t.Fatal("invalid rule returned partial data")
			}
			if tc.valid && !reflect.DeepEqual(payloads["rules"], []string{tc.entry}) {
				t.Fatalf("entry changed: %v", payloads)
			}
		})
	}
}

func TestIOSResolverStillRejectsAndroidOnlyEntries(t *testing.T) {
	for _, entry := range []string{"PROCESS-NAME,app.exe", "NETWORK,UDP", "AND,((NETWORK,TCP),(DST-PORT,443))"} {
		profile := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "rules", Type: "inline", Behavior: "classical", Payload: []string{entry}}}}
		if _, _, err := ResolveMobileProviders(context.Background(), profile); err == nil {
			t.Fatalf("iOS accepted %q", entry)
		}
	}
}

func TestIOSProvidersProjectOnlyUDP443AndPreserveInput(t *testing.T) {
	for _, providerType := range []string{"inline", "http"} {
		t.Run(providerType, func(t *testing.T) {
			quic := "AND,((DOMAIN-SUFFIX,openai.com),(NETWORK,UDP),(DST-PORT,443))"
			bare := "AND,((DST-PORT,443),(NETWORK,UDP)),REJECT"
			mixed := []string{"DOMAIN,first.org,DIRECT", quic, "IP-CIDR,192.0.2.0/24,Japan Proxy,no-resolve", bare, "DST-PORT,8443"}
			profile := subscription.RoutingProfile{
				Final: "DIRECT", BlockQUIC: true,
				Providers: []subscription.RoutingProvider{
					{Key: "before", Type: "inline", Behavior: "domain", Payload: []string{"+.example.org"}},
					{Key: "quic_selected", Type: providerType, Behavior: "classical", Payload: []string{quic, bare}},
					{Key: "mixed", Type: providerType, Behavior: "classical", Payload: mixed},
					{Key: "after", Type: "inline", Behavior: "ipcidr", Payload: []string{"2001:db8::/32"}},
				},
				Rules: []subscription.RoutingRule{
					{Type: "DOMAIN", Value: "first.org", Target: "DIRECT"},
					{Type: "RULE-SET", Value: "quic_selected", Target: "REJECT"},
					{Type: "RULE-SET", Value: "mixed", Target: "Japan Proxy"},
					{Type: "RULE-SET", Value: "quic_selected", Target: "DIRECT", NoResolve: true},
					{Type: "RULE-SET", Value: "after", Target: "DIRECT", NoResolve: true},
				},
			}
			if providerType == "http" {
				for i := 1; i <= 2; i++ {
					profile.Providers[i].URL = "https://cdn.jsdelivr.net/" + profile.Providers[i].Key + ".list"
					profile.Providers[i].Format = "text"
					profile.Providers[i].Interval = 86400
				}
			}
			fetch := func(_ context.Context, url, _ string) (Contents, error) {
				payload := mixed
				if url == profile.Providers[1].URL {
					payload = profile.Providers[1].Payload
				}
				return Contents{Entries: payload, Count: len(payload)}, nil
			}
			got, payloads, err := resolveMobileProviders(context.Background(), profile, fetch)
			if err != nil {
				t.Fatal(err)
			}
			wantMixed := []string{mixed[0], mixed[2], mixed[4]}
			wantProvider := profile.Providers[2]
			wantProvider.Type, wantProvider.URL, wantProvider.Format, wantProvider.Interval = "inline", "", "", 0
			wantProvider.Payload = wantMixed
			want := profile
			want.Providers = []subscription.RoutingProvider{profile.Providers[0], wantProvider, profile.Providers[3]}
			want.Rules = []subscription.RoutingRule{profile.Rules[0], profile.Rules[2], profile.Rules[4]}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(payloads, map[string][]string{"before": {"+.example.org"}, "mixed": wantMixed, "after": {"2001:db8::/32"}}) {
				t.Fatalf("incorrect iOS projection: %+v %v", got, payloads)
			}
			android, androidPayloads, err := resolveMobileProfile(context.Background(), profile, fetch, true)
			if err != nil || !reflect.DeepEqual(android, profile) || !reflect.DeepEqual(androidPayloads["mixed"], mixed) || !reflect.DeepEqual(androidPayloads["quic_selected"], []string{quic, bare}) {
				t.Fatalf("Android changed: %+v %v %v", android, androidPayloads, err)
			}
			got.Rules[0].Value = "changed.org"
			got.Providers[1].Payload[0] = "DOMAIN,changed.org"
			payloads["mixed"][0] = "DOMAIN,also-changed.org"
			if len(profile.Providers) != 4 || len(profile.Rules) != 5 || profile.Rules[0].Value != "first.org" || !reflect.DeepEqual(profile.Providers[1].Payload, []string{quic, bare}) || !reflect.DeepEqual(profile.Providers[2].Payload, []string{"DOMAIN,first.org,DIRECT", quic, "IP-CIDR,192.0.2.0/24,Japan Proxy,no-resolve", bare, "DST-PORT,8443"}) {
				t.Fatal("input profile mutated")
			}
		})
	}
}

func TestIOSProvidersFailClosedForUnprojectableAND(t *testing.T) {
	for _, entry := range []string{
		"AND,((NETWORK,TCP),(DST-PORT,443))",
		"AND,((DOMAIN,example.org),(DST-PORT,443))",
		"AND,((NETWORK,UDP),(DST-PORT,53))",
		"AND,((NETWORK,UDP),(DST-PORT,443-444))",
		"AND,((NETWORK,UDP),(DST-PORT,443)",
		"AND,((NETWORK,UDP),(DST-PORT,443))),REJECT",
		"AND,((NETWORK,UDP),(DST-PORT,443)),",
		"AND,((NETWORK,UDP),(DST-PORT,443)),REJECT,extra",
		"AND,((NETWORK,UDP),(DST-PORT,443)),no-resolve",
		"AND,((NETWORK,UDP),(DST-PORT,443),(PROCESS-NAME,app.exe))",
		"AND,((NETWORK,UDP),(DST-PORT,443),(DOMAIN,bad/domain))",
		"AND,((NETWORK,UDP),(DST-PORT,443),(NETWORK,TCP))",
		"AND,((NETWORK,UDP),(DST-PORT,443),(DST-PORT,8443))",
		"AND,((NETWORK,UDP),(DST-PORT,443),(OR,((DOMAIN,a.org),(DOMAIN,b.org))))",
		"AND,((NETWORK,UDP),(DST-PORT,443))\n",
		"AND,((NETWORK,UDP),(DST-PORT,443))," + strings.Repeat("x", 512),
	} {
		t.Run(entry, func(t *testing.T) {
			profile := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{{Key: "bad", Type: "inline", Behavior: "classical", Payload: []string{"AND,((NETWORK,UDP),(DST-PORT,443))", entry}}}}
			got, payloads, err := ResolveMobileProviders(context.Background(), profile)
			if err == nil || !strings.Contains(err.Error(), "bad") || payloads != nil || !reflect.DeepEqual(got, subscription.RoutingProfile{}) {
				t.Fatalf("unsupported policy returned partial data: %+v %v %v", got, payloads, err)
			}
		})
	}
}

func TestIOSProvidersRejectDuplicateKeyAfterUDPProjection(t *testing.T) {
	profile := subscription.RoutingProfile{Providers: []subscription.RoutingProvider{
		{Key: "rules", Type: "inline", Behavior: "classical", Payload: []string{"AND,((NETWORK,UDP),(DST-PORT,443))"}},
		{Key: "rules", Type: "inline", Behavior: "domain", Payload: []string{"example.org"}},
	}}
	got, payloads, err := ResolveMobileProviders(context.Background(), profile)
	if err == nil || payloads != nil || !reflect.DeepEqual(got, subscription.RoutingProfile{}) {
		t.Fatalf("duplicate key returned a partial profile: %+v %v %v", got, payloads, err)
	}
}
