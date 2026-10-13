package main

import (
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
	"strings"
	"testing"
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
}
