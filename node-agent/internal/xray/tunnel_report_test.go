package xray

import (
	"net/netip"
	"testing"
	"time"
)

func TestTransportOwnershipRequiresAuthenticatedEvidence(t *testing.T) {
	i := NewAccessIndex()
	peer := TransportPeer{Local: netip.MustParseAddrPort("10.0.2.2:8443"), Remote: netip.MustParseAddrPort("10.0.2.1:40000"), Idle: time.Second}
	if report := joinTransportOwners(i, []TransportPeer{peer}); report.Known {
		t.Fatal("unauthenticated peer yielded a complete observation")
	}
	now := time.Now()
	i.Observe(accessLine(peer.Remote.String(), accessUUID), now)
	report := joinTransportOwners(i, []TransportPeer{peer})
	if !report.Known || len(report.Connections) != 1 {
		t.Fatalf("authenticated peer: %+v", report)
	}
	c := report.Connections[0]
	if c.UUID != accessUUID || c.AcceptedAt != now.UnixMilli() || c.IdleMillis != 1000 {
		t.Fatalf("unexpected transport: %+v", c)
	}
	if report := joinTransportOwners(i, nil); !report.Known || len(report.Connections) != 0 {
		t.Fatal("explicit empty observation not known")
	}
}
