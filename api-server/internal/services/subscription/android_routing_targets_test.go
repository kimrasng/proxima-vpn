package subscription

import (
	"errors"
	"reflect"
	"testing"
)

func TestAndroidRoutingClassicalPreservesExplicitTargetsAndOrder(t *testing.T) {
	for _, providerType := range []string{"inline", "http"} {
		t.Run(providerType, func(t *testing.T) {
			p := androidTestProfile()
			p.Groups = append(p.Groups, RoutingGroup{Key: "jp", Name: "Japan Proxy", Type: "url-test", Members: []RoutingMember{tagged("jp")}})
			payload := []string{
				"DOMAIN-SUFFIX,example.com,DIRECT",
				"DOMAIN,exact.example,DIRECT",
				"DOMAIN-SUFFIX,example.com,REJECT",
				"DOMAIN-SUFFIX,example.com,Japan Proxy",
				"DOMAIN,second.example,jp",
				"DOMAIN,inherited.example",
				"IP-CIDR,10.1.2.3/8,DIRECT,no-resolve",
				"IP-CIDR6,fc00::/7,DIRECT",
				"IP-CIDR,203.0.113.0/24,no-resolve",
				"DST-PORT,443,REJECT",
				"DOMAIN,again.example,DIRECT",
			}
			provider := RoutingProvider{Key: "mixed", Type: providerType, Behavior: "classical"}
			if providerType == "inline" {
				provider.Payload = payload
			} else {
				provider.URL, provider.Format = "https://example.test/mixed", "text"
			}
			p.Providers = []RoutingProvider{provider}
			p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "mixed", Target: "auto"}}

			doc := decodeAndroidRouting(t, p, map[string][]string{"mixed": payload}, androidTestNodes())

			want := []androidRoutingRule{
				{Target: "direct", Domain: []string{"domain:example.com", "full:exact.example"}},
				{Target: "block", Domain: []string{"domain:example.com"}},
				{Target: "proxy", Policy: "jp", Candidates: []string{"JP Tokyo"}, Domain: []string{"domain:example.com", "full:second.example"}},
				{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, Domain: []string{"full:inherited.example"}},
				{Target: "direct", IP: []string{"10.0.0.0/8", "fc00::/7"}},
				{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, IP: []string{"203.0.113.0/24"}},
				{Target: "block", Port: "443"},
				{Target: "direct", Domain: []string{"full:again.example"}},
				{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, Match: true},
			}
			if !reflect.DeepEqual(doc.Rules, want) {
				t.Fatalf("rules = %+v, want %+v", doc.Rules, want)
			}
		})
	}
}

func TestAndroidRoutingExplicitTargetDoesNotResolveUnusedOuterPolicy(t *testing.T) {
	p := androidTestProfile()
	p.Groups = append(p.Groups, RoutingGroup{Key: "empty", Name: "Empty", Type: "select", Members: []RoutingMember{tagged("absent")}})
	p.Providers = []RoutingProvider{{Key: "explicit", Type: "inline", Behavior: "classical", Payload: []string{"DOMAIN,example.com,DIRECT"}}}
	p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "explicit", Target: "empty"}}

	doc := decodeAndroidRouting(t, p, nil, androidTestNodes())

	if !reflect.DeepEqual(doc.Rules[0], androidRoutingRule{Target: "direct", Domain: []string{"full:example.com"}}) {
		t.Fatalf("explicit target inherited empty group: %+v", doc.Rules)
	}
}

func TestAndroidRoutingRejectsInvalidExplicitTargets(t *testing.T) {
	for _, entry := range []string{
		"DOMAIN,example.com,missing", "DOMAIN,example.com,empty",
		"DOMAIN,example.com,auto", // Key/name collision below is ambiguous.
		"DOMAIN,example.com,", "DOMAIN,example.com,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,invalid", "IP-CIDR,10.0.0.0/8,,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve,extra",
	} {
		t.Run(entry, func(t *testing.T) {
			p := androidTestProfile()
			p.Groups = append(p.Groups,
				RoutingGroup{Key: "other", Name: "auto", Type: "select", Members: []RoutingMember{builtin("DIRECT")}},
				RoutingGroup{Key: "empty", Name: "Empty", Type: "select", Members: []RoutingMember{tagged("absent")}},
			)
			p.Providers = []RoutingProvider{{Key: "remote", Type: "http", Behavior: "classical", Format: "text", URL: "https://example.test/remote"}}
			p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "remote", Target: "DIRECT"}}
			data, err := GenerateAndroidRouting(p, map[string][]string{"remote": {entry}}, androidTestNodes())
			var invalid *AndroidRoutingError
			if !errors.As(err, &invalid) || data != nil {
				t.Fatalf("explicit target discarded: data=%s error=%v", data, err)
			}
		})
	}
}

func TestAndroidRoutingBudgetCountsClassicalPolicyBoundaries(t *testing.T) {
	p := androidTestProfile()
	p.Groups = append(p.Groups, RoutingGroup{Key: "second", Name: "Second", Type: "url-test", Members: []RoutingMember{allNodes()}})
	p.Providers = []RoutingProvider{{Key: "remote", Type: "http", Behavior: "classical", Format: "text", URL: "https://example.test/remote"}}
	p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "remote", Target: "DIRECT"}}
	payload := make([]string, 100000)
	for i := range payload {
		payload[i] = "DOMAIN,example.com,auto"
		if i%2 == 1 {
			payload[i] = "DOMAIN,example.com,second"
		}
	}
	data, err := GenerateAndroidRouting(p, map[string][]string{"remote": payload}, androidTestNodes())
	var budget *AndroidRoutingError
	if !errors.As(err, &budget) || budget.Path != "budget" || data != nil {
		t.Fatalf("policy amplification: data length=%d error=%v", len(data), err)
	}
}
