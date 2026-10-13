package main

import (
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

func TestLiveTopologyPreservesExactSourcePort(t *testing.T) {
	base := runtimeConfig(map[string]string{"uuid-a": "secret"})
	config := strings.Replace(base, `"user":["uuid-a@proxima"]`, `"user":["uuid-a@proxima"],"source":["203.0.113.2/32"],"sourcePort":["40000"]`, 1)
	topology, err := parseLiveTopology([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range topology.rules {
		if len(rule.SourcePorts) == 1 && rule.SourcePorts[0] == 40000 {
			found = true
		}
	}
	if !found {
		t.Fatal("source port lost on live parse")
	}
	if sameRules(topology.rules, []xray.RoutingRule{{OutboundTag: "irrelevant"}}) {
		t.Fatal("rule comparison did not detect change")
	}
	for _, port := range []string{"0", "65536", "40000-40001", "040000"} {
		if _, err := parseLiveTopology([]byte(strings.Replace(config, "40000", port, 1))); err == nil {
			t.Errorf("accepted invalid source port %q", port)
		}
	}
	for name, source := range map[string]string{
		"mapped ipv4":        "::ffff:203.0.113.2/128",
		"mapped unspecified": "::ffff:0.0.0.0/128",
		"mapped multicast":   "::ffff:224.0.0.1/128",
	} {
		bad := strings.Replace(config, `203.0.113.2/32`, source, 1)
		if _, err := parseLiveTopology([]byte(bad)); err == nil {
			t.Errorf("accepted %s source", name)
		}
	}
}

func TestLiveTopologyPreservesSessionQualifiedRules(t *testing.T) {
	token := strings.Repeat("a", 64)
	config := `{"outbounds":[{"tag":"device-egress-uuid-a~` + token + `","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":10086,"users":[{"user":"uuid-a","pass":"secret"}]}]}}],"routing":{"rules":[{"type":"field","user":["uuid-a@proxima"],"source":["203.0.113.2/32"],"sourcePort":["40000"],"ruleTag":"session-` + token + `","sessionToken":"` + token + `","outboundTag":"device-egress-uuid-a~` + token + `"}]}}`

	topology, err := parseLiveTopology([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.rules) != 1 || topology.rules[0].SessionToken != token || topology.rules[0].SourcePorts[0] != 40000 {
		t.Fatalf("topology=%+v", topology)
	}
	bad := strings.Replace(config, "session-"+token, "wrong", 1)
	if _, err := parseLiveTopology([]byte(bad)); err == nil {
		t.Fatal("mismatched session rule tag accepted")
	}
	bad = strings.Replace(config, `"sourcePort":["40000"]`, `"sourcePort":["40000-40001"]`, 1)
	if _, err := parseLiveTopology([]byte(bad)); err == nil {
		t.Fatal("session source port range accepted")
	}
	bad = strings.Replace(config, `"uuid-a@proxima"`, `"uuid-b@proxima"`, 1)
	if _, err := parseLiveTopology([]byte(bad)); err == nil {
		t.Fatal("session rule bound to another authenticated user")
	}
	bad = strings.Replace(config, `"user":"uuid-a"`, `"user":"uuid-b"`, 1)
	if _, err := parseLiveTopology([]byte(bad)); err == nil {
		t.Fatal("session outbound bound to another SOCKS credential")
	}
	bad = strings.Replace(config, `"outboundTag":"device-egress-uuid-a~`+token+`"`, `"outboundTag":"missing-`+token+`"`, 1)
	if _, err := parseLiveTopology([]byte(bad)); err == nil {
		t.Fatal("session rule with missing outbound accepted")
	}
	bad = strings.Replace(config, `"port":10086`, `"port":10087`, 1)
	if _, err := parseLiveTopology([]byte(bad)); err == nil {
		t.Fatal("session outbound with non-local endpoint accepted")
	}
}
