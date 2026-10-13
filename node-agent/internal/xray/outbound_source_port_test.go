package xray

import (
	"encoding/binary"
	"testing"
)

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
