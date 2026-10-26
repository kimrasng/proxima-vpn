package subscription

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	krHomeID = "11111111-1111-1111-1111-111111111111"
	krIDCID  = "22222222-2222-2222-2222-222222222222"
	jpID     = "33333333-3333-3333-3333-333333333333"
)

func routingNodes() []NodeInfo {
	return []NodeInfo{
		{Name: "KR Home 1", IP: "203.0.113.1", Port: 443, Protocol: "vless_reality", NodeID: krHomeID, Tags: []string{"kr-home"}},
		{Name: "KR IDC 1", IP: "203.0.113.2", Port: 443, Protocol: "trojan_tls", NodeID: krIDCID, Tags: []string{"kr"}},
		{Name: "JP Tokyo 1", IP: "203.0.113.3", Port: 8388, Protocol: "shadowsocks", SSPassword: "AAAAAAAAAAAAAAAAAAAAAA==", NodeID: jpID, Tags: []string{"jp"}},
		{Name: "JP UDP", IP: "203.0.113.4", Port: 443, Protocol: "hysteria2", NodeID: jpID, Tags: []string{"jp"}},
	}
}

func renderRouted(t *testing.T, nodes []NodeInfo, p RoutingProfile) map[string]any {
	t.Helper()
	out, err := GenerateRoutedClash(nodes, "uuid", []string{"info"}, p)
	if err != nil {
		t.Fatalf("GenerateRoutedClash: %v", err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(out, &config); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return config
}

func groupMembers(t *testing.T, config map[string]any) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, g := range config["proxy-groups"].([]any) {
		m := g.(map[string]any)
		var names []string
		for _, p := range m["proxies"].([]any) {
			names = append(names, p.(string))
		}
		out[m["name"].(string)] = names
	}
	return out
}

func ruleLines(config map[string]any) []string {
	var out []string
	for _, r := range config["rules"].([]any) {
		out = append(out, r.(string))
	}
	return out
}

func TestDefaultRoutingProfileIsValid(t *testing.T) {
	if errs := ValidateRoutingProfile(DefaultRoutingProfile()); len(errs) != 0 {
		t.Fatalf("default profile invalid: %+v", errs)
	}
}

func TestRoutedClashDefaultLayout(t *testing.T) {
	config := renderRouted(t, routingNodes(), DefaultRoutingProfile())
	groups := groupMembers(t, config)

	want := map[string][]string{
		"🏠 한국 가정망":  {"KR Home 1"},
		"🇰🇷 한국 전체":  {"KR Home 1", "KR IDC 1"},
		"🇯🇵 일본 자동":  {"JP Tokyo 1"},
		"🇰🇷 한국 서비스": {"🏠 한국 가정망", "🇰🇷 한국 전체", "🇯🇵 일본 자동", "DIRECT"},
		"🛑 광고 차단":   {"🌐 해외 트래픽", "REJECT"},
	}
	for name, members := range want {
		if !reflect.DeepEqual(groups[name], members) {
			t.Errorf("group %s = %v, want %v", name, groups[name], members)
		}
	}
	first := config["proxy-groups"].([]any)[0].(map[string]any)["name"]
	if first != groupInfo {
		t.Errorf("first group = %v, want info group", first)
	}

	rules := ruleLines(config)
	if rules[0] != "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT" {
		t.Errorf("first rule = %q, want QUIC reject", rules[0])
	}
	if last := rules[len(rules)-1]; last != "MATCH,🌐 해외 트래픽" {
		t.Errorf("last rule = %q", last)
	}
	if !slicesContain(rules, "RULE-SET,kr_ip,🇰🇷 한국 서비스") || !slicesContain(rules, "RULE-SET,private_ip,DIRECT,no-resolve") {
		t.Errorf("missing expected rules: %v", rules)
	}

	providers := config["rule-providers"].(map[string]any)
	naver := providers["kr_naver"].(map[string]any)
	if naver["format"] != "mrs" || naver["proxy"] != "DIRECT" || naver["path"] != "./ruleset/kr_naver.mrs" {
		t.Errorf("kr_naver provider = %v", naver)
	}
	if _, ok := providers["kr_custom"].(map[string]any)["payload"]; !ok {
		t.Errorf("inline provider lost its payload")
	}
}

func TestRoutedClashDropsEmptyGroupsAndRetargets(t *testing.T) {
	nodes := []NodeInfo{{Name: "JP Tokyo 1", IP: "203.0.113.3", Port: 443, Protocol: "vless_reality", NodeID: jpID, Tags: []string{"jp"}}}
	groups := groupMembers(t, renderRouted(t, nodes, DefaultRoutingProfile()))

	for _, gone := range []string{"🏠 한국 가정망", "🇰🇷 한국 전체"} {
		if _, ok := groups[gone]; ok {
			t.Errorf("group %s should be dropped without KR nodes", gone)
		}
	}
	if got, want := groups["🇰🇷 한국 서비스"], []string{"🇯🇵 일본 자동", "DIRECT"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kr_service = %v, want %v", got, want)
	}

	p := DefaultRoutingProfile()
	p.Groups = append(p.Groups, RoutingGroup{Key: "us", Name: "US", Type: "select", Members: []RoutingMember{tagged("us")}})
	p.Rules = append(p.Rules, RoutingRule{Type: "DOMAIN", Value: "example.com", Target: "us"})
	rules := ruleLines(renderRouted(t, nodes, p))
	if !slicesContain(rules, "DOMAIN,example.com,🌐 해외 트래픽") {
		t.Errorf("rule to an empty group must fall back to final: %v", rules)
	}
}

func TestRoutedClashKeepsTCPOnly(t *testing.T) {
	config := renderRouted(t, routingNodes(), DefaultRoutingProfile())
	for _, p := range config["proxies"].([]any) {
		m := p.(map[string]any)
		if m["type"] == "hysteria2" || m["udp"] != false {
			t.Errorf("proxy %v must be TCP-only", m["name"])
		}
	}
}

func TestValidateRoutingProfileRejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(*RoutingProfile)
		path   string
	}{
		"missing target":  {func(p *RoutingProfile) { p.Rules[0].Target = "nope" }, "rules[0].target"},
		"comma in name":   {func(p *RoutingProfile) { p.Groups[0].Name = "a,b" }, "groups[0].name"},
		"reserved name":   {func(p *RoutingProfile) { p.Groups[0].Name = "direct" }, "groups[0].name"},
		"bad key":         {func(p *RoutingProfile) { p.Groups[0].Key = "Bad Key" }, "groups[0].key"},
		"unknown ruleset": {func(p *RoutingProfile) { p.Rules[0].Value = "missing" }, "rules[0].value"},
		"ipv4 as cidr6": {func(p *RoutingProfile) {
			p.Rules = append(p.Rules, RoutingRule{Type: "IP-CIDR6", Value: "10.0.0.0/8", Target: "DIRECT"})
		}, "rules[8].value"},
		"bad port": {func(p *RoutingProfile) {
			p.Rules = append(p.Rules, RoutingRule{Type: "DST-PORT", Value: "70000", Target: "DIRECT"})
		}, "rules[8].value"},
		"no-resolve on domain": {func(p *RoutingProfile) {
			p.Rules = append(p.Rules, RoutingRule{Type: "DOMAIN", Value: "a.com", Target: "DIRECT", NoResolve: true})
		}, "rules[8].no_resolve"},
		"mrs classical":  {func(p *RoutingProfile) { p.Providers[0].Behavior = "classical" }, "providers[0].format"},
		"http url":       {func(p *RoutingProfile) { p.Providers[0].URL = "http://x" }, "providers[0].url"},
		"bad payload":    {func(p *RoutingProfile) { p.Providers[3].Payload = []string{"a,b"} }, "providers[3].payload[0]"},
		"final missing":  {func(p *RoutingProfile) { p.Final = "gone" }, "final"},
		"self reference": {func(p *RoutingProfile) { p.Groups[0].Members = []RoutingMember{groupRef("kr_service")} }, "groups[0].members[0].ref"},
		"cycle": {func(p *RoutingProfile) {
			p.Groups[5].Members = append(p.Groups[5].Members, groupRef("kr_service"))
		}, "groups"},
		"bad tag":     {func(p *RoutingProfile) { p.Groups[3].Members[0].Tag = "KR Home" }, "groups[3].members[0].tag"},
		"bad node id": {func(p *RoutingProfile) { p.Groups[3].Members = []RoutingMember{{Kind: MemberNode, NodeID: "nope"}} }, "groups[3].members[0].node_id"},
		"bad kind":    {func(p *RoutingProfile) { p.Groups[3].Members[0].Kind = "nodes" }, "groups[3].members[0].kind"},
		"dns mode": {func(p *RoutingProfile) {
			s := DefaultClashSettings()
			s.DNS.EnhancedMode = "magic"
			p.Settings = &s
		}, "settings.dns.enhanced_mode"},
		"default nameserver host": {func(p *RoutingProfile) {
			s := DefaultClashSettings()
			s.DNS.DefaultNameserver = []string{"dns.google"}
			p.Settings = &s
		}, "settings.dns.default_nameserver"},
		"extra yaml owns rules": {func(p *RoutingProfile) {
			s := DefaultClashSettings()
			s.ExtraYAML = "rules:\n  - MATCH,DIRECT\n"
			p.Settings = &s
		}, "settings.extra_yaml"},
		"extra yaml not mapping": {func(p *RoutingProfile) {
			s := DefaultClashSettings()
			s.ExtraYAML = "- a\n- b\n"
			p.Settings = &s
		}, "settings.extra_yaml"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := DefaultRoutingProfile()
			tc.mutate(&p)
			errs := ValidateRoutingProfile(p)
			for _, e := range errs {
				if e.Path == tc.path {
					return
				}
			}
			t.Fatalf("want error at %s, got %+v", tc.path, errs)
		})
	}
}

// TestRoutedClashLoadsInMihomo runs the real core's config check when a mihomo
// binary is available (MIHOMO_BIN), catching anything the core would reject.
func TestRoutedClashLoadsInMihomo(t *testing.T) {
	bin := os.Getenv("MIHOMO_BIN")
	if bin == "" {
		t.Skip("MIHOMO_BIN not set")
	}
	out, err := GenerateRoutedClash(routingNodes(), "00000000-0000-0000-0000-000000000001", []string{"info"}, DefaultRoutingProfile())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := exec.Command(bin, "-d", dir, "-t", "-f", path).CombinedOutput()
	if err != nil || !strings.Contains(string(res), "test is successful") {
		t.Fatalf("mihomo rejected config: %v\n%s", err, res)
	}
}

func TestRoutedClashDirectNodeMember(t *testing.T) {
	p := DefaultRoutingProfile()
	p.Groups = append(p.Groups, RoutingGroup{Key: "pinned", Name: "📌 Pinned", Type: "select",
		Members: []RoutingMember{{Kind: MemberNode, NodeID: krIDCID}, builtin("DIRECT")}})
	groups := groupMembers(t, renderRouted(t, routingNodes(), p))
	if got, want := groups["📌 Pinned"], []string{"KR IDC 1", "DIRECT"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pinned = %v, want %v", got, want)
	}
	if got := groups["🚀 노드 선택"]; !slicesContain(got, "KR Home 1") || !slicesContain(got, "JP Tokyo 1") {
		t.Errorf("all-nodes member missing proxies: %v", got)
	}
}

func TestRoutedClashSettingsAndExtraYAML(t *testing.T) {
	p := DefaultRoutingProfile()
	s := DefaultClashSettings()
	s.General.MixedPort = 7999
	s.General.LogLevel = "info"
	s.DNS.EnhancedMode = "redir-host"
	s.DNS.NameserverPolicy = map[string][]string{"+.naver.com": {"https://1.1.1.1/dns-query"}}
	s.Hosts = map[string]string{"router.lan": "192.168.0.1"}
	s.ExtraYAML = "tun:\n  enable: false\n  stack: mixed\ndns:\n  cache-algorithm: arc\n"
	p.Settings = &s
	if errs := ValidateRoutingProfile(p); len(errs) != 0 {
		t.Fatalf("settings invalid: %+v", errs)
	}
	config := renderRouted(t, routingNodes(), p)
	if config["mixed-port"] != 7999 || config["log-level"] != "info" {
		t.Errorf("general settings not applied: mixed-port=%v log-level=%v", config["mixed-port"], config["log-level"])
	}
	dns := config["dns"].(map[string]any)
	if dns["enhanced-mode"] != "redir-host" || dns["cache-algorithm"] != "arc" {
		t.Errorf("dns = %v", dns)
	}
	if _, ok := dns["fake-ip-range"]; ok {
		t.Error("fake-ip-range must be omitted outside fake-ip mode")
	}
	if _, ok := dns["nameserver-policy"].(map[string]any)["+.naver.com"]; !ok {
		t.Errorf("nameserver-policy missing: %v", dns["nameserver-policy"])
	}
	if config["hosts"].(map[string]any)["router.lan"] != "192.168.0.1" {
		t.Errorf("hosts = %v", config["hosts"])
	}
	if config["tun"].(map[string]any)["stack"] != "mixed" {
		t.Errorf("extra yaml tun not merged: %v", config["tun"])
	}
}

func slicesContain(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
