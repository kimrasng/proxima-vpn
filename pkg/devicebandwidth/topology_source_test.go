package devicebandwidth

import (
	"encoding/json"
	"testing"
)

func TestExplicitAddressDenyIsLiveManagedNotStructuralFallback(t *testing.T) {
	for _, data := range []struct {
		raw  string
		want bool
	}{
		{`{"type":"field","user":["uuid@proxima"],"outboundTag":"block","ruleTag":"client-address-deny-uuid"}`, true},
		{`{"type":"field","inboundTag":["vless-in"],"outboundTag":"block"}`, false},
		{`{"type":"field","user":["other@proxima"],"outboundTag":"block","ruleTag":"client-address-deny-uuid"}`, false},
	} {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data.raw), &rule); err != nil {
			t.Fatal(err)
		}
		if got := IsDeviceRule(rule); got != data.want {
			t.Fatalf("rule=%s got=%v", data.raw, got)
		}
	}
}

func TestAddressQualifiedRuleIsLiveTopologyOnlyWithExactSource(t *testing.T) {
	for _, test := range []struct {
		source string
		want   bool
	}{{"203.0.113.2/32", true}, {"2001:db8::2/128", true}, {"203.0.113.0/24", false}, {"invalid", false}} {
		var rule map[string]json.RawMessage
		data := `{"type":"field","user":["uuid@proxima"],"outboundTag":"device-egress-uuid~0123456789abcdef","source":["` + test.source + `"]}`
		if err := json.Unmarshal([]byte(data), &rule); err != nil {
			t.Fatal(err)
		}
		if got := IsDeviceRule(rule); got != test.want {
			t.Fatalf("source=%s got=%v", test.source, got)
		}
	}
}
