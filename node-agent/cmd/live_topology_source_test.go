package main

import (
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

func TestLiveTopologyPreservesSourceQualifiedRules(t *testing.T) {
	config := runtimeConfig(map[string]string{"uuid-a": "secret"})
	config = strings.Replace(config, `"user":["uuid-a@proxima"]`, `"user":["uuid-a@proxima"],"source":["203.0.113.2/32"]`, 1)
	parsed, err := parseLiveTopology([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range parsed.rules {
		if len(rule.SourceCIDRs) == 1 && rule.SourceCIDRs[0] == "203.0.113.2/32" {
			found = true
		}
	}
	if !found {
		t.Fatal("source condition discarded")
	}
	if sameRules([]xray.RoutingRule{{SourceCIDRs: []string{"203.0.113.2/32"}}}, []xray.RoutingRule{{SourceCIDRs: []string{"203.0.113.3/32"}}}) {
		t.Fatal("source change ignored")
	}
	bad := strings.Replace(config, "203.0.113.2/32", "not-a-cidr", 1)
	if _, err := parseLiveTopology([]byte(bad)); err == nil {
		t.Fatal("invalid source accepted")
	}
}
