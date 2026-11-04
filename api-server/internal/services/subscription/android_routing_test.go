package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func androidTestProfile() RoutingProfile {
	return RoutingProfile{
		Final:  "auto",
		Groups: []RoutingGroup{{Key: "auto", Name: "Automatic", Type: "url-test", Members: []RoutingMember{allNodes()}}},
	}
}

func androidTestNodes() []NodeInfo {
	return []NodeInfo{
		{Name: "KR home", Protocol: "vless_reality", NodeID: krHomeID, Tags: []string{"kr-home"}},
		{Name: "JP Tokyo", Protocol: "vless_reality", NodeID: jpID, Tags: []string{"jp"}},
		{Name: "JP non-VLESS", Protocol: "trojan_tls", NodeID: jpID, Tags: []string{"jp"}},
	}
}

func decodeAndroidRouting(t *testing.T, profile RoutingProfile, payloads map[string][]string, nodes []NodeInfo) androidRoutingDocument {
	t.Helper()
	data, err := GenerateAndroidRouting(profile, payloads, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var doc androidRoutingDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestAndroidRoutingPreservesOrderedExpansionAndFinal(t *testing.T) {
	p := androidTestProfile()
	p.BlockQUIC = true
	p.Groups[0].URL = "https://probe.example/check"
	p.Providers = []RoutingProvider{
		{Key: "private", Type: "http", Behavior: "domain", Format: "mrs", URL: "https://example.test/private.mrs"},
		{Key: "regional", Type: "inline", Behavior: "classical", Payload: []string{
			"DOMAIN-SUFFIX,kr", "DOMAIN,service.example", "IP-CIDR,203.0.113.5/24,no-resolve", "IP-CIDR6,2001:db8::1/32", "DOMAIN-KEYWORD,bank", "DST-PORT,8000-8100",
		}},
	}
	p.Rules = []RoutingRule{
		{Type: "DOMAIN", Value: "exception.example", Target: "REJECT"},
		{Type: "RULE-SET", Value: "private", Target: "DIRECT"},
		{Type: "RULE-SET", Value: "regional", Target: "auto"},
		{Type: "DOMAIN-SUFFIX", Value: "last.example", Target: "DIRECT"},
	}

	doc := decodeAndroidRouting(t, p, map[string][]string{
		"private":  {" # comment", "localhost", "+.lan", "", "// comment"},
		"regional": {"DOMAIN,override.example"}, // Inline data remains authoritative.
	}, androidTestNodes())

	want := []androidRoutingRule{
		{Target: "block", Network: "udp", Port: "443"},
		{Target: "block", Domain: []string{"full:exception.example"}},
		{Target: "direct", Domain: []string{"full:localhost", "domain:lan"}},
		{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, Domain: []string{"domain:kr", "full:service.example"}},
		{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, IP: []string{"203.0.113.0/24", "2001:db8::/32"}},
		{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, Domain: []string{"bank"}},
		{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, Port: "8000-8100"},
		{Target: "direct", Domain: []string{"domain:last.example"}},
		{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, Match: true},
	}
	if !reflect.DeepEqual(doc.Rules, want) || doc.Version != 1 || doc.AutoTestURL != "https://probe.example/check" {
		t.Fatalf("document = %+v; want rules %+v, version 1 and custom URL", doc, want)
	}
}

func TestAndroidRoutingSelectPreservesFirstLiveBranch(t *testing.T) {
	for _, target := range []string{"DIRECT", "REJECT", "auto"} {
		t.Run(target, func(t *testing.T) {
			p := androidTestProfile()
			first := builtin(target)
			if target == "auto" {
				first = groupRef("auto")
			}
			p.Groups = append(p.Groups,
				RoutingGroup{Key: "empty", Name: "Empty", Type: "fallback", Members: []RoutingMember{tagged("missing")}},
				RoutingGroup{Key: "chosen", Name: "Chosen", Type: "select", Members: []RoutingMember{groupRef("empty"), first, builtin("REJECT"), allNodes()}},
			)
			p.Final = "chosen"

			doc := decodeAndroidRouting(t, p, nil, androidTestNodes())

			want := androidRoutingRule{Target: "proxy", Policy: "auto", Candidates: []string{"KR home", "JP Tokyo"}, Match: true}
			if target == "DIRECT" {
				want = androidRoutingRule{Target: "direct", Match: true}
			} else if target == "REJECT" {
				want = androidRoutingRule{Target: "block", Match: true}
			}
			if !reflect.DeepEqual(doc.Rules, []androidRoutingRule{want}) {
				t.Fatalf("rules = %+v, want %+v", doc.Rules, want)
			}
		})
	}
}

func TestAndroidRoutingCandidatesUseTagsNodeIDsAndExactNames(t *testing.T) {
	p := androidTestProfile()
	p.Groups[0].Members = []RoutingMember{tagged("jp"), {Kind: MemberNode, NodeID: krHomeID}, allNodes()}
	nodes := androidTestNodes()
	nodes[0].Name = "KR home #1 / exact name"

	doc := decodeAndroidRouting(t, p, nil, nodes)

	if want := []string{"JP Tokyo", "KR home #1 / exact name"}; !reflect.DeepEqual(doc.Rules[0].Candidates, want) {
		t.Fatalf("candidates = %v, want %v", doc.Rules[0].Candidates, want)
	}
}

func TestAndroidRoutingRejectsEmptyTarget(t *testing.T) {
	p := androidTestProfile()
	p.Groups = append(p.Groups, RoutingGroup{Key: "empty", Name: "Empty", Type: "select", Members: []RoutingMember{tagged("absent")}})
	p.Providers = []RoutingProvider{{Key: "empty", Type: "http", Behavior: "domain", Format: "text", URL: "https://example.test/empty"}}
	p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "empty", Target: "REJECT"}, {Type: "DOMAIN", Value: "example.com", Target: "empty"}}

	if _, err := GenerateAndroidRouting(p, map[string][]string{"empty": {}}, androidTestNodes()); err == nil {
		t.Fatal("empty rule target must not fall back to another policy")
	}
}

func TestAndroidRoutingRejectsNoVLESSNodes(t *testing.T) {
	p := androidTestProfile()
	if _, err := GenerateAndroidRouting(p, nil, []NodeInfo{{Name: "Trojan", Protocol: "trojan_tls"}}); err == nil {
		t.Fatal("missing VLESS nodes must not fall back to DIRECT")
	}
}

func TestAndroidRoutingDefaultProfileKeepsKoreanAndOverseasPolicies(t *testing.T) {
	p := DefaultRoutingProfile()
	payloads := map[string][]string{
		"private_domain": {"localhost", "+.lan"},
		"private_ip":     {"10.0.0.0/8", "fc00::/7"},
		"ads":            {"+.ads.example"},
		"kr_naver":       {"+.naver.com"},
		"kr_kakao":       {"+.kakao.com"},
		"kr_coupang":     {"+.coupang.com"},
		"kr_ip":          {"203.0.113.0/24"},
	}

	doc := decodeAndroidRouting(t, p, payloads, androidTestNodes())

	if len(doc.Rules) != 10 {
		t.Fatalf("got %d rules, want QUIC + eight providers + final", len(doc.Rules))
	}
	for i, rule := range doc.Rules {
		switch {
		case i == 0:
			if rule.Target != "block" || rule.Network != "udp" || rule.Port != "443" {
				t.Fatalf("QUIC rule = %+v", rule)
			}
		case i == 1 || i == 2:
			if rule.Target != "direct" || rule.Policy != "" {
				t.Fatalf("private rule = %+v", rule)
			}
		case i == 3 || i == 9:
			// Ads are opt-in; their default branch is overseas, not REJECT.
			if rule.Target != "proxy" || rule.Policy != "jp_auto" || !reflect.DeepEqual(rule.Candidates, []string{"JP Tokyo"}) {
				t.Fatalf("overseas/ads rule = %+v", rule)
			}
		default:
			if rule.Target != "proxy" || rule.Policy != "kr_home" || !reflect.DeepEqual(rule.Candidates, []string{"KR home"}) {
				t.Fatalf("Korean service rule = %+v", rule)
			}
		}
	}
}

func TestAndroidRoutingDefaultProfileBalancesOnlyPreferredHomeGroup(t *testing.T) {
	for _, blockQUIC := range []bool{false, true} {
		p := DefaultRoutingProfile()
		p.BlockQUIC = blockQUIC
		payloads := map[string][]string{}
		for _, provider := range p.Providers {
			if provider.Type == "http" {
				if provider.Behavior == "ipcidr" {
					payloads[provider.Key] = []string{"203.0.113.0/24"}
				} else {
					payloads[provider.Key] = []string{"+.example.com"}
				}
			}
		}
		nodes := append(androidTestNodes(),
			NodeInfo{Name: "KR home second", Protocol: "vless_reality", NodeID: krHomeID, Tags: []string{"kr-home"}},
			NodeInfo{Name: "KR datacenter", Protocol: "vless_reality", NodeID: krIDCID, Tags: []string{"kr"}},
		)

		doc := decodeAndroidRouting(t, p, payloads, nodes)

		offset := 0
		if blockQUIC {
			offset = 1
			if doc.Rules[0].Network != "udp" || doc.Rules[0].Port != "443" || doc.Rules[0].Target != "block" {
				t.Fatalf("QUIC rule = %+v", doc.Rules[0])
			}
		}
		if len(doc.Rules) != 9+offset {
			t.Fatalf("BlockQUIC=%t: rules = %+v", blockQUIC, doc.Rules)
		}
		for i, rule := range doc.Rules[offset:] {
			switch {
			case i < 2:
				if rule.Target != "direct" {
					t.Fatalf("private rule = %+v", rule)
				}
			case i == 2 || i == 8:
				if rule.Policy != "jp_auto" || !reflect.DeepEqual(rule.Candidates, []string{"JP Tokyo"}) {
					t.Fatalf("overseas/ads rule = %+v", rule)
				}
			default:
				if rule.Target != "proxy" || rule.Policy != "kr_home" || !reflect.DeepEqual(rule.Candidates, []string{"KR home", "KR home second"}) {
					t.Fatalf("home policy must exclude lower-priority KR/JP groups: %+v", rule)
				}
			}
		}
	}
}

func TestAndroidRoutingFallbackUsesProbeURLAndRejectsBuiltinMixing(t *testing.T) {
	for _, builtinTarget := range []string{"", "DIRECT", "REJECT"} {
		t.Run(builtinTarget, func(t *testing.T) {
			p := androidTestProfile()
			p.Groups[0].Type = "fallback"
			p.Groups[0].URL = "https://fallback.example/probe"
			if builtinTarget != "" {
				p.Groups[0].Members = append(p.Groups[0].Members, builtin(builtinTarget))
			}
			data, err := GenerateAndroidRouting(p, nil, androidTestNodes())
			if builtinTarget != "" {
				var unsupported *AndroidRoutingError
				if !errors.As(err, &unsupported) || data != nil {
					t.Fatalf("mixed builtin returned data=%s err=%v", data, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var doc androidRoutingDocument
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.AutoTestURL != "https://fallback.example/probe" || !reflect.DeepEqual(doc.Rules[0].Candidates, []string{"KR home", "JP Tokyo"}) {
				t.Fatalf("fallback metadata = %+v", doc)
			}
		})
	}
}

func TestAndroidRoutingRejectsUnrepresentableProfilesWithoutPartialData(t *testing.T) {
	cases := map[string]func(*RoutingProfile, *[]NodeInfo){
		"automatic direct": func(p *RoutingProfile, _ *[]NodeInfo) {
			p.Groups[0].Members = append(p.Groups[0].Members, builtin("DIRECT"))
		},
		"automatic reject": func(p *RoutingProfile, _ *[]NodeInfo) {
			p.Groups[0].Members = append(p.Groups[0].Members, builtin("REJECT"))
		},
		"nested automatic": func(p *RoutingProfile, _ *[]NodeInfo) {
			p.Groups = append(p.Groups, RoutingGroup{Key: "outer", Name: "Outer", Type: "url-test", Members: []RoutingMember{groupRef("auto"), tagged("jp")}})
			p.Final = "outer"
		},
		"conflicting probes": func(p *RoutingProfile, _ *[]NodeInfo) {
			p.Groups = append(p.Groups, RoutingGroup{Key: "other", Name: "Other", Type: "url-test", URL: "https://other.test/probe", Members: []RoutingMember{allNodes()}})
			p.Rules = []RoutingRule{{Type: "DOMAIN", Value: "example.com", Target: "other"}}
		},
		"missing provider": func(p *RoutingProfile, _ *[]NodeInfo) {
			p.Providers = []RoutingProvider{{Key: "remote", Type: "http", Behavior: "ipcidr", Format: "mrs", URL: "https://example.test/ip.mrs"}}
			p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "remote", Target: "DIRECT"}}
		},
		"duplicate names": func(_ *RoutingProfile, n *[]NodeInfo) { (*n)[1].Name = (*n)[0].Name },
		"reserved name":   func(_ *RoutingProfile, n *[]NodeInfo) { (*n)[0].Name = "DIRECT" },
		"empty name":      func(_ *RoutingProfile, n *[]NodeInfo) { (*n)[0].Name = "" },
		"cycle":           func(p *RoutingProfile, _ *[]NodeInfo) { p.Groups[0].Members = []RoutingMember{groupRef("auto")} },
		"unknown target": func(p *RoutingProfile, _ *[]NodeInfo) {
			p.Rules = []RoutingRule{{Type: "DOMAIN", Value: "example.com", Target: "missing"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p, nodes := androidTestProfile(), androidTestNodes()
			mutate(&p, &nodes)
			data, err := GenerateAndroidRouting(p, nil, nodes)
			var unsupported *AndroidRoutingError
			if !errors.As(err, &unsupported) || data != nil {
				t.Fatalf("data = %s, error = %v; want typed error and no partial output", data, err)
			}
		})
	}
}

func TestAndroidRoutingProviderRejectsUnsupportedEntries(t *testing.T) {
	for _, tc := range []struct{ behavior, entry string }{
		{"domain", "geosite:private"}, {"domain", "*.example.com"}, {"domain", ".example.com"},
		{"ipcidr", "geoip:private"}, {"ipcidr", "192.168.1.1"},
		{"classical", "GEOSITE,kr"}, {"classical", "GEOIP,KR"}, {"classical", "SRC-PORT,443"},
		{"classical", "DOMAIN,example.com,UNKNOWN"}, {"classical", "DOMAIN,example.com,no-resolve"},
		{"classical", "MATCH"}, {"classical", "DST-PORT,65536"}, {"classical", "IP-CIDR6,10.0.0.0/8"},
	} {
		t.Run(tc.behavior+"/"+tc.entry, func(t *testing.T) {
			p := androidTestProfile()
			p.Providers = []RoutingProvider{{Key: "remote", Type: "http", Behavior: tc.behavior, Format: "text", URL: "https://example.test/rules"}}
			p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "remote", Target: "DIRECT"}}
			data, err := GenerateAndroidRouting(p, map[string][]string{"remote": {"# comment", tc.entry}}, androidTestNodes())
			var unsupported *AndroidRoutingError
			if !errors.As(err, &unsupported) || data != nil {
				t.Fatalf("data = %s, error = %v", data, err)
			}
		})
	}
}

func TestAndroidRoutingIsDeterministicAndDoesNotMutateInputs(t *testing.T) {
	p, nodes := androidTestProfile(), androidTestNodes()
	p.Providers = []RoutingProvider{{Key: "ip", Type: "inline", Behavior: "ipcidr", Payload: []string{"10.3.2.1/8", "fc00::/7"}}}
	p.Rules = []RoutingRule{{Type: "RULE-SET", Value: "ip", Target: "DIRECT", NoResolve: true}}
	before, err := json.Marshal([]any{p, nodes})
	if err != nil {
		t.Fatal(err)
	}
	a, err := GenerateAndroidRouting(p, nil, nodes)
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateAndroidRouting(p, nil, nodes)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal([]any{p, nodes})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) || !bytes.Equal(before, after) {
		t.Fatal("generation changed its inputs or produced nondeterministic output")
	}
}
