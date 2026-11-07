package subscription

import (
	"errors"
	"reflect"
	"testing"
)

func TestAndroidRoutingClassicalANDPreservesConjunctionAndInheritedTarget(t *testing.T) {
	for _, providerType := range []string{"inline", "http"} {
		t.Run(providerType, func(t *testing.T) {
			p := androidTestProfile()
			payload := []string{
				"DOMAIN,before.example",
				"AND,((DOMAIN-SUFFIX,example.com),(NETWORK,UDP),(DST-PORT,443))",
				"AND,((DOMAIN-SUFFIX,example.com),(NETWORK,TCP),(DST-PORT,443))",
				"AND,((DOMAIN-SUFFIX,example.com),(NETWORK,TCP),(DST-PORT,8443))",
				"DOMAIN,after.example",
				"AND,((IP-CIDR,10.0.0.0/8),(NETWORK,UDP),(DST-PORT,53))",
				"IP-CIDR,192.168.0.0/16",
				"AND,((IP-CIDR,172.16.0.0/12),(DST-PORT,443))",
			}
			provider := RoutingProvider{Key: "conjunction", Type: providerType, Behavior: "classical"}
			if providerType == "inline" {
				provider.Payload = payload
			} else {
				provider.URL, provider.Format = "https://example.test/rules", "text"
			}
			p.Providers = []RoutingProvider{provider}
			p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "conjunction", Target: "auto"}}

			doc := decodeAndroidRouting(t, p, map[string][]string{"conjunction": payload}, androidTestNodes())

			want := []androidRoutingRule{
				{Domain: []string{"full:before.example"}},
				{Domain: []string{"domain:example.com"}, Network: "udp", Port: "443"},
				{Domain: []string{"domain:example.com"}, Network: "tcp", Port: "443"},
				{Domain: []string{"domain:example.com"}, Network: "tcp", Port: "8443"},
				{Domain: []string{"full:after.example"}},
				{IP: []string{"10.0.0.0/8"}, Network: "udp", Port: "53"},
				{IP: []string{"192.168.0.0/16"}},
				{IP: []string{"172.16.0.0/12"}, Port: "443"},
				{Match: true},
			}
			for i := range want {
				want[i].Target, want[i].Policy = "proxy", "auto"
				want[i].Candidates = []string{"KR home", "JP Tokyo"}
			}
			if !reflect.DeepEqual(doc.Rules, want) {
				t.Fatalf("rules = %+v, want %+v", doc.Rules, want)
			}
		})
	}
}

func TestAndroidRoutingClassicalANDPreservesExplicitTarget(t *testing.T) {
	p := androidTestProfile()
	p.Providers = []RoutingProvider{{Key: "conjunction", Type: "inline", Behavior: "classical", Payload: []string{
		"AND,((DOMAIN-SUFFIX,example.com),(NETWORK,UDP),(DST-PORT,443)),REJECT",
	}}}
	p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "conjunction", Target: "auto"}}

	doc := decodeAndroidRouting(t, p, nil, androidTestNodes())

	want := androidRoutingRule{Target: "block", Domain: []string{"domain:example.com"}, Network: "udp", Port: "443"}
	if !reflect.DeepEqual(doc.Rules[0], want) {
		t.Fatalf("rule = %+v, want %+v", doc.Rules[0], want)
	}
}

func TestAndroidRoutingClassicalANDRejectsUnrepresentableConditions(t *testing.T) {
	for _, entry := range []string{
		"AND,((DOMAIN,example.com),(NETWORK,UDP),(DST-PORT,443)",
		"AND,((DOMAIN,example.com),(NETWORK,UDP))),REJECT",
		"AND,(DOMAIN,example.com),(NETWORK,UDP)",
		"AND,((DOMAIN,example.com))",
		"AND,((DOMAIN,example.com),NETWORK,UDP)",
		"AND,((DOMAIN,example.com),(DOMAIN-SUFFIX,example.com))",
		"AND,((IP-CIDR,10.0.0.0/8),(IP-CIDR,192.168.0.0/16))",
		"AND,((NETWORK,UDP),(NETWORK,TCP))",
		"AND,((DST-PORT,443),(DST-PORT,8443))",
		"AND,((NETWORK,ICMP),(DST-PORT,443))",
		"AND,((NETWORK,UDP),(DST-PORT,65536))",
		"AND,((DOMAIN,example.com,DIRECT),(NETWORK,UDP))",
		"AND,((DOMAIN,example.com),(SRC-PORT,443))",
		"AND,((NETWORK,UDP),(DST-PORT,443)),no-resolve",
		"AND,((AND,((NETWORK,UDP),(DST-PORT,443))),(DOMAIN,example.com))",
	} {
		t.Run(entry, func(t *testing.T) {
			p := androidTestProfile()
			p.Providers = []RoutingProvider{{Key: "remote", Type: "http", Behavior: "classical", Format: "text", URL: "https://example.test/rules"}}
			p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "remote", Target: "auto"}}

			data, err := GenerateAndroidRouting(p, map[string][]string{"remote": {entry}}, androidTestNodes())

			var invalid *AndroidRoutingError
			if !errors.As(err, &invalid) || data != nil {
				t.Fatalf("data = %s, error = %v; want typed error and no partial output", data, err)
			}
		})
	}
}
