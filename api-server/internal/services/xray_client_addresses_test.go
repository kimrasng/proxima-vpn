package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestClientAddressAllowlistReplacesSharedRouteAndPreservesOthers(t *testing.T) {
	base, err := WithDeviceEgressRouting(egressFixture(t, nil, []string{egressDeviceA, egressDeviceB}))
	if err != nil {
		t.Fatal(err)
	}
	config, err := WithClientAddressAllowlist(base, map[string][]string{egressDeviceA: {"203.0.113.2", "203.0.113.3"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(config, &cfg); err != nil {
		t.Fatal(err)
	}
	var out []struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(cfg["outbounds"], &out); err != nil {
		t.Fatal(err)
	}
	qualified := 0
	other := false
	for _, o := range out {
		if o.Tag == devicebandwidth.OutboundPrefix+egressDeviceA {
			t.Fatal("shared catchall outbound retained")
		}
		if strings.HasPrefix(o.Tag, devicebandwidth.OutboundPrefix+egressDeviceA+"~") {
			qualified++
		}
		if o.Tag == devicebandwidth.OutboundPrefix+egressDeviceB {
			other = true
		}
	}
	if qualified != 2 || !other {
		t.Fatalf("qualified=%d other=%v", qualified, other)
	}
	if !strings.Contains(string(config), "203.0.113.2/32") {
		t.Fatal("exact source condition missing")
	}
	denied, err := WithClientAddressAllowlist(base, map[string][]string{egressDeviceA: {}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(denied), devicebandwidth.OutboundPrefix+egressDeviceA) {
		t.Fatal("empty allowlist retained egress")
	}
	if !strings.Contains(string(denied), "client-address-deny-"+egressDeviceA) {
		t.Fatal("explicit credential deny absent")
	}
}

func TestClientAddressAllowlistRejectsUnknownCredentialAndMissingFallback(t *testing.T) {
	base, err := WithDeviceEgressRouting(egressFixture(t, nil, []string{egressDeviceA}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithClientAddressAllowlist(base, map[string][]string{egressDeviceB: {"203.0.113.2"}}); err == nil {
		t.Fatal("unknown credential accepted")
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(base, &cfg); err != nil {
		t.Fatal(err)
	}
	var routing map[string]json.RawMessage
	if err := json.Unmarshal(cfg["routing"], &routing); err != nil {
		t.Fatal(err)
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(routing["rules"], &rules); err != nil {
		t.Fatal(err)
	}
	rules = rules[:len(rules)-1]
	routing["rules"], _ = json.Marshal(rules)
	cfg["routing"], _ = json.Marshal(routing)
	missing, _ := json.Marshal(cfg)
	if _, err := WithClientAddressAllowlist(missing, map[string][]string{egressDeviceA: {"203.0.113.2"}}); err == nil {
		t.Fatal("missing fallback accepted")
	}
}
