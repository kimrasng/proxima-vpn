package subscription

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func iosRoutingProfile() RoutingProfile {
	return RoutingProfile{
		Final: "manual",
		Groups: []RoutingGroup{
			{Key: "manual", Name: "Manual", Type: "select", Members: []RoutingMember{groupRef("auto"), allNodes(), builtin("DIRECT")}},
			{Key: "auto", Name: "Automatic", Type: "url-test", Hidden: true, Members: []RoutingMember{tagged("jp")}},
			{Key: "ads", Name: "Ads", Type: "select", Members: []RoutingMember{builtin("REJECT"), groupRef("manual")}},
		},
		Providers: []RoutingProvider{
			{Key: "z_domains", Type: "http", Behavior: "domain", Format: "yaml", URL: "https://example.com/domains.yaml"},
			{Key: "a_ips", Type: "http", Behavior: "ipcidr", Format: "text", URL: "https://example.com/ips.txt"},
			{Key: "custom", Type: "inline", Behavior: "classical", Payload: []string{"DOMAIN,ads.example.com", "IP-CIDR,10.0.0.0/8,no-resolve", "IP-CIDR6,fc00::/7", "DST-PORT,8000-8100"}},
		},
		Rules: []RoutingRule{
			{Type: "RULE-SET", Value: "custom", Target: "ads"},
			{Type: "RULE-SET", Value: "a_ips", Target: "DIRECT", NoResolve: true},
			{Type: "RULE-SET", Value: "z_domains", Target: "auto"},
			{Type: "DOMAIN-SUFFIX", Value: "example.com", Target: "manual"},
		},
	}
}

func renderIOS(t *testing.T, nodes []NodeInfo, profile RoutingProfile) map[string]any {
	t.Helper()
	out, err := GenerateIOSClash(nodes, "test-user", profile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(out, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestIOSClashPreservesRoutingAndCompatibleProxies(t *testing.T) {
	nodes := append(routingNodes(), NodeInfo{Name: "VMess", IP: "203.0.113.5", Port: 443, Protocol: "vmess_ws"},
		NodeInfo{Name: "WG", Protocol: "wireguard"}, NodeInfo{Name: "TUIC", Protocol: "tuic"}, NodeInfo{Name: "HY", Protocol: "hysteria"})
	config := renderIOS(t, nodes, iosRoutingProfile())
	var names, types []string
	for _, proxy := range config["proxies"].([]any) {
		p := proxy.(map[string]any)
		names = append(names, p["name"].(string))
		types = append(types, p["type"].(string))
		if p["udp"] != false {
			t.Errorf("proxy %s offers UDP", p["name"])
		}
	}
	if want := []string{"KR Home 1", "KR IDC 1", "JP Tokyo 1", "VMess"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("proxy order = %v, want %v", names, want)
	}
	if want := []string{"vless", "trojan", "ss", "vmess"}; !reflect.DeepEqual(types, want) {
		t.Fatalf("proxy types = %v, want %v", types, want)
	}
	groups := groupMembers(t, config)
	if want := []string{"Automatic", "KR Home 1", "KR IDC 1", "JP Tokyo 1", "VMess", "DIRECT"}; !reflect.DeepEqual(groups["Manual"], want) {
		t.Errorf("manual members = %v, want %v", groups["Manual"], want)
	}
	var groupOrder []string
	for _, group := range config["proxy-groups"].([]any) {
		groupOrder = append(groupOrder, group.(map[string]any)["name"].(string))
	}
	if want := []string{"Manual", "Automatic", "Ads"}; !reflect.DeepEqual(groupOrder, want) {
		t.Errorf("group order = %v, want %v", groupOrder, want)
	}
	wantRules := []string{"RULE-SET,custom,Ads", "RULE-SET,a_ips,DIRECT,no-resolve", "RULE-SET,z_domains,Automatic", "DOMAIN-SUFFIX,example.com,Manual", "MATCH,Manual"}
	if got := ruleLines(config); !reflect.DeepEqual(got, wantRules) {
		t.Errorf("rules = %v, want %v", got, wantRules)
	}
}

func TestIOSClashKeepsProviderContentsAndOrder(t *testing.T) {
	profile := iosRoutingProfile()
	out, err := GenerateIOSClash(routingNodes(), "test-user", profile)
	if err != nil {
		t.Fatal(err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for i := 0; i < len(root.Content[0].Content); i += 2 {
		if root.Content[0].Content[i].Value == "rule-providers" {
			providers := root.Content[0].Content[i+1].Content
			for j := 0; j < len(providers); j += 2 {
				keys = append(keys, providers[j].Value)
			}
		}
	}
	if want := []string{"z_domains", "a_ips", "custom"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("provider order = %v, want %v", keys, want)
	}
	var config map[string]any
	if err := yaml.Unmarshal(out, &config); err != nil {
		t.Fatal(err)
	}
	providers := config["rule-providers"].(map[string]any)
	if p := providers["a_ips"].(map[string]any); p["format"] != "text" || p["url"] != "https://example.com/ips.txt" || p["path"] != "./ruleset/a_ips.list" {
		t.Errorf("text provider changed: %v", p)
	}
	if got := providers["custom"].(map[string]any)["payload"]; !reflect.DeepEqual(got, []any{"DOMAIN,ads.example.com", "IP-CIDR,10.0.0.0/8,no-resolve", "IP-CIDR6,fc00::/7", "DST-PORT,8000-8100"}) {
		t.Errorf("classical conditions changed: %v", got)
	}
}

func TestIOSClashPreservesClassicalTarget(t *testing.T) {
	profile := iosRoutingProfile()
	profile.Providers[2].Payload = []string{"DOMAIN-SUFFIX,example.com,PROXY"}
	out, err := GenerateIOSClash(routingNodes(), "00000000-0000-0000-0000-000000000001", profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "DOMAIN-SUFFIX,example.com,PROXY") {
		t.Fatalf("classical target missing: %s", out)
	}
}

func TestIOSClashAcceptsResolvedLargeProvider(t *testing.T) {
	profile := iosRoutingProfile()
	profile.Providers[0] = RoutingProvider{Key: "z_domains", Type: "inline", Behavior: "domain", Payload: make([]string, 5001)}
	for i := range profile.Providers[0].Payload {
		profile.Providers[0].Payload[i] = fmt.Sprintf("d%d.example", i)
	}
	if _, err := GenerateIOSClash(routingNodes(), "00000000-0000-0000-0000-000000000001", profile); err != nil {
		t.Fatal(err)
	}
}

func TestIOSClashStripsUnsupportedOptionsAfterExtraYAML(t *testing.T) {
	profile := iosRoutingProfile()
	settings := DefaultClashSettings()
	settings.ExtraYAML = "tun:\n  enable: true\ngeox-url:\n  geoip: https://example.com/geoip.dat\ndns:\n  enhanced-mode: fake-ip\n  nameserver: [9.9.9.9]\n  nameserver-policy:\n    geosite:cn: [1.1.1.1]\n  fallback: [8.8.8.8]\n  fallback-filter:\n    geoip: true\n    geoip-code: CN\n    ipcidr: [240.0.0.0/4]\nprofile:\n  store-fake-ip: true\n"
	profile.Settings = &settings
	config := renderIOS(t, routingNodes(), profile)
	for _, key := range []string{"tun", "sniffer", "geox-url", "tcp-concurrent", "find-process-mode", "external-controller"} {
		if _, exists := config[key]; exists {
			t.Errorf("unsupported top-level option %s", key)
		}
	}
	dns := config["dns"].(map[string]any)
	for _, key := range []string{"fake-ip-range", "fake-ip-filter", "fake-ip-filter-mode", "nameserver-policy", "respect-rules", "proxy-server-nameserver"} {
		if _, exists := dns[key]; exists {
			t.Errorf("unsupported DNS option %s", key)
		}
	}
	if dns["enhanced-mode"] != "redir-host" || !reflect.DeepEqual(dns["nameserver"], []any{"9.9.9.9"}) {
		t.Errorf("compatible DNS settings = %v", dns)
	}
	if want := map[string]any{"geoip": false, "ipcidr": []any{"240.0.0.0/4"}}; !reflect.DeepEqual(dns["fallback-filter"], want) {
		t.Errorf("fallback filter = %v, want %v", dns["fallback-filter"], want)
	}
	if _, exists := config["profile"].(map[string]any)["store-fake-ip"]; exists {
		t.Error("fake-IP persistence retained")
	}
	reality := config["proxies"].([]any)[0].(map[string]any)["reality-opts"].(map[string]any)
	if _, exists := reality["support-x25519mlkem768"]; exists {
		t.Error("unsupported Reality extension retained")
	}
}

func TestIOSClashRejectsSemanticIncompatibilities(t *testing.T) {
	cases := []struct {
		name string
		edit func(*RoutingProfile)
		path string
	}{
		{"QUIC", func(p *RoutingProfile) { p.BlockQUIC = true }, "block_quic"},
		{"mrs format", func(p *RoutingProfile) { p.Providers[0].Format = "mrs" }, "providers[0].format"},
		{"mrs URL", func(p *RoutingProfile) { p.Providers[0].URL = "https://example.com/rules.MRS?version=1" }, "providers[0].url"},
		{"empty final", func(p *RoutingProfile) { p.Groups[0].Members = []RoutingMember{tagged("missing")} }, "groups[0].members"},
		{"empty target", func(p *RoutingProfile) { p.Groups[2].Members = []RoutingMember{tagged("missing")} }, "groups[2].members"},
		{"missing target", func(p *RoutingProfile) { p.Rules[0].Target = "missing" }, "rules[0].target"},
		{"name collision", func(p *RoutingProfile) { p.Groups[0].Name = "KR Home 1" }, "groups[0].name"},
		{"extra routing", func(p *RoutingProfile) {
			s := DefaultClashSettings()
			s.ExtraYAML = "rules: [MATCH,DIRECT]"
			p.Settings = &s
		}, "settings.extra_yaml"},
	}
	for _, kind := range []string{"GEOIP", "GEOSITE", "AND", "OR", "NOT", "SRC-PORT", "NETWORK"} {
		cases = append(cases, struct {
			name string
			edit func(*RoutingProfile)
			path string
		}{
			kind, func(p *RoutingProfile) { p.Rules[0] = RoutingRule{Type: kind, Value: "443", Target: "DIRECT"} }, "rules[0].type",
		})
		cases = append(cases, struct {
			name string
			edit func(*RoutingProfile)
			path string
		}{
			"classical " + kind, func(p *RoutingProfile) { p.Providers[2].Payload = []string{kind + ",443"} }, "providers[2].payload[0]",
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := iosRoutingProfile()
			tc.edit(&p)
			out, err := GenerateIOSClash(routingNodes(), "test-user", p)
			var incompatible *IOSClashError
			if !errors.As(err, &incompatible) || incompatible.Path != tc.path || out != nil {
				t.Fatalf("got (%q, %v), want nil output and error at %s", out, err, tc.path)
			}
		})
	}
}

func TestIOSClashDoesNotMutateInputAndIsDeterministic(t *testing.T) {
	profile := iosRoutingProfile()
	settings := DefaultClashSettings()
	settings.Hosts = map[string]string{"router.lan": "192.168.0.1"}
	settings.ExtraYAML = "hosts:\n  router.lan: 192.168.0.2\n"
	profile.Settings = &settings
	before, err := yaml.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	first, err := GenerateIOSClash(routingNodes(), "test-user", profile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateIOSClash(routingNodes(), "test-user", profile)
	if err != nil {
		t.Fatal(err)
	}
	after, err := yaml.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !bytes.Equal(first, second) {
		t.Fatal("rendering mutated input or produced unstable output")
	}
}

func TestIOSClashRejectsNoCompatibleProxies(t *testing.T) {
	out, err := GenerateIOSClash([]NodeInfo{{Name: "UDP", Protocol: "hysteria2"}}, "test-user", iosRoutingProfile())
	if err == nil || out != nil {
		t.Fatalf("got (%q, %v), want error and no config", out, err)
	}
}
