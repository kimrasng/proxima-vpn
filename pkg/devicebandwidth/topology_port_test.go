package devicebandwidth

import (
	"encoding/json"
	"testing"
)

func TestConnectionQualifiedRuleIsStrippableOnlyWithExactPeerPort(t *testing.T) {
	for _, item := range []struct {
		ports string
		want  bool
	}{{`["40000"]`, true}, {`["0"]`, false}, {`["65536"]`, false}, {`["40000","40001"]`, false}, {`["40000-40001"]`, false}} {
		var rule map[string]json.RawMessage
		raw := `{"type":"field","user":["uuid@proxima"],"source":["203.0.113.2/32"],"sourcePort":` + item.ports + `,"outboundTag":"device-egress-uuid~0123456789abcdef"}`
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			t.Fatal(err)
		}
		if got := IsDeviceRule(rule); got != item.want {
			t.Errorf("ports=%s accepted=%v", item.ports, got)
		}
	}
}
