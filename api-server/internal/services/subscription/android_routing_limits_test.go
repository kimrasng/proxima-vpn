package subscription

import (
	"errors"
	"strings"
	"testing"
)

func TestAndroidRoutingBoundsRepeatedProviderExpansion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries int
		value   string
		refs    int
	}{
		{"repeated entry count", 30000, "+.example.com", 9},
		{"repeated output bytes", 1000, "+." + strings.Repeat("a", 200) + ".com", 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := androidTestProfile()
			p.Providers = []RoutingProvider{{Key: "large", Type: "http", Behavior: "domain", Format: "text", URL: "https://example.test/large"}}
			payload := make([]string, tc.entries)
			for i := range payload {
				payload[i] = tc.value
			}
			p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "large", Target: "auto"}}
			// The provider is valid and permitted alone; repetition is what
			// crosses the output budget, not provider syntax or profile limits.
			if _, err := GenerateAndroidRouting(p, map[string][]string{"large": payload}, androidTestNodes()); err != nil {
				t.Fatalf("single provider rejected: %v", err)
			}
			for len(p.Rules) < tc.refs {
				p.Rules = append(p.Rules, p.Rules[0])
			}
			data, err := GenerateAndroidRouting(p, map[string][]string{"large": payload}, androidTestNodes())
			var budget *AndroidRoutingError
			if !errors.As(err, &budget) || budget.Path != "budget" || data != nil {
				t.Fatalf("repeated provider: data length=%d, error=%v", len(data), err)
			}
		})
	}
}

func TestAndroidRoutingBudgetCountsRepeatedCandidates(t *testing.T) {
	p := androidTestProfile()
	p.Providers = []RoutingProvider{{Key: "alternating", Type: "http", Behavior: "classical", Format: "text", URL: "https://example.test/alternating"}}
	p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "alternating", Target: "auto"}}
	payload := make([]string, 2000)
	for i := range payload {
		payload[i] = "DOMAIN,example.com"
		if i%2 == 1 {
			payload[i] = "IP-CIDR,203.0.113.0/24"
		}
	}
	nodes := make([]NodeInfo, 100)
	for i := range nodes {
		nodes[i] = NodeInfo{Name: strings.Repeat("a", 100) + string(rune('A'+i)), Protocol: "vless_reality"}
	}
	data, err := GenerateAndroidRouting(p, map[string][]string{"alternating": payload}, nodes)
	var budget *AndroidRoutingError
	if !errors.As(err, &budget) || budget.Path != "budget" || data != nil {
		t.Fatalf("candidate amplification: data length=%d, error=%v", len(data), err)
	}
}

func TestAndroidRoutingBudgetRunsBeforeProviderParsing(t *testing.T) {
	p := androidTestProfile()
	p.Providers = []RoutingProvider{{Key: "oversized", Type: "http", Behavior: "classical", Format: "text", URL: "https://example.test/oversized"}}
	p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "oversized", Target: "auto"}}
	// Parsing this malformed provider would otherwise allocate a large split
	// result. The budget must reject it before the classical parser runs.
	entry := strings.Repeat(",", maxAndroidRoutingBytes/6+1)
	data, err := GenerateAndroidRouting(p, map[string][]string{"oversized": {entry}}, androidTestNodes())
	var budget *AndroidRoutingError
	if !errors.As(err, &budget) || budget.Path != "budget" || data != nil {
		t.Fatalf("oversized payload reached parsing: data length=%d, error=%v", len(data), err)
	}
}
