package devicebandwidth

import (
	"encoding/json"
	"testing"
)

func TestSessionEgressDeclarationBindsExactTransportToken(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	out, rule, err := SessionEgressDeclarations(id, "203.0.113.2", 40000, token)
	if err != nil {
		t.Fatal(err)
	}
	var outbound map[string]any
	_ = json.Unmarshal(out, &outbound)
	if outbound["tag"] != OutboundPrefix+id+"~"+token {
		t.Fatalf("tag=%v", outbound["tag"])
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(rule, &fields)
	encodedToken := ""
	if json.Unmarshal(fields["sessionToken"], &encodedToken) != nil || encodedToken != token || !IsDeviceRule(fields) {
		t.Fatalf("session rule=%s", rule)
	}
	if _, _, err := SessionEgressDeclarations(id, "203.0.113.2", 40000, "bad"); err == nil {
		t.Fatal("invalid session key accepted")
	}
}
