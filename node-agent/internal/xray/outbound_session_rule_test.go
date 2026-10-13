package xray

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
)

func TestReplaceRoutingRulesRejectsIPv4MappedExactSources(t *testing.T) {
	client := &StatsClient{}
	for _, source := range []string{"::ffff:203.0.113.2/128", "::ffff:0.0.0.0/128", "::ffff:224.0.0.1/128"} {
		err := client.ReplaceRoutingRules(context.Background(), []RoutingRule{{OutboundTag: "session-egress", SourceCIDRs: []string{source}, SourcePorts: []uint16{40000}, SessionToken: strings.Repeat("a", 64)}})
		if err == nil {
			t.Errorf("accepted mapped IPv4 source %q", source)
		}
	}
}

func TestSessionRuleEncodesTokenOnlyInSupportedRuleTagAndExactTuple(t *testing.T) {
	token := strings.Repeat("a", 64)
	fields := decodeFields(t, marshalRoutingRule(RoutingRule{OutboundTag: "session-egress", UserEmail: []string{"uuid@proxima"}, SourceCIDRs: []string{"203.0.113.2/32"}, SourcePorts: []uint16{40000}, SessionToken: token}))
	if string(fields[19][0]) != "session-"+token {
		t.Fatalf("ruleTag=%q", fields[19][0])
	}
	if _, exists := fields[14]; exists {
		t.Fatal("invented unsupported protobuf field emitted")
	}
	ports := decodeFields(t, fields[16][0])
	ranges := decodeFields(t, ports[1][0])
	from, _ := binary.Uvarint(ranges[1][0])
	to, _ := binary.Uvarint(ranges[2][0])
	if from != 40000 || to != 40000 {
		t.Fatalf("source range=%d-%d", from, to)
	}
}
