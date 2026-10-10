package devicebandwidth

import (
	"encoding/json"
	"testing"
)

const topologyFixture = `{
  "api": {"tag": "api"},
  "inbounds": [{"tag": "vless-in", "protocol": "vless"}],
  "outbounds": [
    {"protocol": "freedom", "tag": "direct"},
    {"protocol": "blackhole", "tag": "block"},
    {"protocol": "socks", "tag": "device-egress-aaa", "settings": {}},
    {"protocol": "socks", "tag": "device-egress-bbb", "settings": {}}
  ],
  "routing": {"rules": [
    {"type": "field", "inboundTag": ["api"], "outboundTag": "api"},
    {"type": "field", "user": ["aaa@proxima"], "outboundTag": "device-egress-aaa"},
    {"type": "field", "user": ["bbb@proxima"], "outboundTag": "device-egress-bbb"},
    {"type": "field", "inboundTag": ["vless-in"], "outboundTag": "block"}
  ]}
}`

func strip(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := StripDeviceTopology(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestStripDeviceTopologyKeepsStructureInOrder(t *testing.T) {
	cfg := strip(t, topologyFixture)
	var outbounds []struct{ Tag string }
	_ = json.Unmarshal(cfg["outbounds"], &outbounds)
	if len(outbounds) != 2 || outbounds[0].Tag != "direct" || outbounds[1].Tag != "block" {
		t.Fatalf("outbounds = %+v, want direct, block", outbounds)
	}
	var routing struct {
		Rules []struct{ OutboundTag string }
	}
	_ = json.Unmarshal(cfg["routing"], &routing)
	if len(routing.Rules) != 2 || routing.Rules[0].OutboundTag != "api" || routing.Rules[1].OutboundTag != "block" {
		t.Fatalf("rules = %+v, want api then the deny fallback", routing.Rules)
	}
}

func TestStripDeviceTopologyMakesDeviceSetsEquivalent(t *testing.T) {
	// Given the same config with one device fewer
	fewer := `{
  "api": {"tag": "api"},
  "inbounds": [{"tag": "vless-in", "protocol": "vless"}],
  "outbounds": [
    {"protocol": "freedom", "tag": "direct"},
    {"protocol": "blackhole", "tag": "block"},
    {"protocol": "socks", "tag": "device-egress-aaa", "settings": {}}
  ],
  "routing": {"rules": [
    {"type": "field", "inboundTag": ["api"], "outboundTag": "api"},
    {"type": "field", "user": ["aaa@proxima"], "outboundTag": "device-egress-aaa"},
    {"type": "field", "inboundTag": ["vless-in"], "outboundTag": "block"}
  ]}
}`
	a, _ := json.Marshal(strip(t, topologyFixture))
	b, _ := json.Marshal(strip(t, fewer))
	if string(a) != string(b) {
		t.Fatalf("device sets differ structurally:\n%s\n%s", a, b)
	}
}

func TestIsDeviceRuleRejectsAnythingBroader(t *testing.T) {
	cases := map[string]bool{
		`{"type":"field","user":["aaa@proxima"],"outboundTag":"device-egress-aaa"}`:                           true,
		`{"type":"field","user":["aaa@proxima"],"outboundTag":"device-egress-aaa","ruleTag":"x"}`:             true,
		`{"type":"field","user":["bbb@proxima"],"outboundTag":"device-egress-aaa"}`:                           false,
		`{"type":"field","user":["aaa@proxima","b@proxima"],"outboundTag":"device-egress-aaa"}`:               false,
		`{"type":"field","user":["aaa@proxima"],"inboundTag":["vless-in"],"outboundTag":"device-egress-aaa"}`: false,
		`{"type":"field","user":["aaa@proxima"],"outboundTag":"direct"}`:                                      false,
		`{"type":"field","user":["@proxima"],"outboundTag":"device-egress-"}`:                                 false,
	}
	for raw, want := range cases {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			t.Fatal(err)
		}
		if got := IsDeviceRule(rule); got != want {
			t.Errorf("IsDeviceRule(%s) = %v, want %v", raw, got, want)
		}
	}
}
