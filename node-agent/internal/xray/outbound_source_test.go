package xray

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSourceQualifiedRuleUsesPinnedIPRuleSchema(t *testing.T) {
	fields := decodeFields(t, marshalRoutingRule(RoutingRule{OutboundTag: "source-egress", UserEmail: []string{"credential@proxima"}, SourceCIDRs: []string{"203.0.113.2/32", "2001:db8::2/128"}}))
	if len(fields[11]) != 2 {
		t.Fatalf("source rule count=%d", len(fields[11]))
	}
	for n, want := range []struct {
		ip   []byte
		bits uint64
	}{{[]byte{203, 0, 113, 2}, 32}, {[]byte{0x20, 1, 0xd, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}, 128}} {
		// IPRule.custom (2) -> CIDRRule.cidr (1) -> CIDR.
		cidr := decodeFields(t, decodeFields(t, decodeFields(t, fields[11][n])[2][0])[1][0])
		if !bytes.Equal(cidr[1][0], want.ip) {
			t.Fatalf("source IP=%x", cidr[1][0])
		}
		bits, _ := binary.Uvarint(cidr[2][0])
		if bits != want.bits {
			t.Fatalf("prefix=%d", bits)
		}
	}
}
