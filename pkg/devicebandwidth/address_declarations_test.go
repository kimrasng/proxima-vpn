package devicebandwidth

import (
	"encoding/json"
	"testing"
)

func TestAddressDeclarationsNeverEmitSharedCatchall(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	outbounds, rules, err := AddressEgressDeclarations(id, []string{"203.0.113.3", "203.0.113.2", "::ffff:203.0.113.2"})
	if err != nil || len(outbounds) != 2 || len(rules) != 2 {
		t.Fatalf("declarations: %d %d %v", len(outbounds), len(rules), err)
	}
	for _, raw := range rules {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rule); err != nil {
			t.Fatal(err)
		}
		if !IsDeviceRule(rule) || rule["source"] == nil {
			t.Fatal("emitted unsafe unqualified rule")
		}
	}
	emptyOut, emptyRules, err := AddressEgressDeclarations(id, nil)
	if err != nil || len(emptyOut) != 0 || len(emptyRules) != 0 {
		t.Fatal("empty allowlist emitted route")
	}
}
