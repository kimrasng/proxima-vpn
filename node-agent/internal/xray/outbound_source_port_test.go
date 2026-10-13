package xray

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestSessionTokenEncodesAsXrayRuleTagNotPortList(t *testing.T) {
	token := strings.Repeat("a", 64)
	fields := decodeFields(t, marshalRoutingRule(RoutingRule{OutboundTag: "session", SourceCIDRs: []string{"10.0.2.1/32"}, SourcePorts: []uint16{40000}, SessionToken: token}))
	if len(fields[16]) != 1 {
		t.Fatalf("sourcePort=%d", len(fields[16]))
	}
	if string(fields[19][0]) != "session-"+token {
		t.Fatalf("ruleTag=%q", fields[19][0])
	}
	if _, present := fields[14]; present {
		t.Fatal("nonexistent Xray protobuf field 14 was emitted")
	}
}

func TestSourcePortsEncodeExactRanges(t *testing.T) {
	fields := decodeFields(t, marshalRoutingRule(RoutingRule{OutboundTag: "cohort", SourceCIDRs: []string{"10.0.2.1/32"}, SourcePorts: []uint16{40000, 40001}}))
	ports := decodeFields(t, fields[16][0])
	if len(ports[1]) != 2 {
		t.Fatalf("port ranges=%d", len(ports[1]))
	}
	for n, expected := range []uint64{40000, 40001} {
		span := decodeFields(t, ports[1][n])
		from, _ := binary.Uvarint(span[1][0])
		to, _ := binary.Uvarint(span[2][0])
		if from != expected || to != expected {
			t.Fatalf("range=%d-%d", from, to)
		}
	}
}
