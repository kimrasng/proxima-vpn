package xray

import (
	"net/netip"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
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
	exitID := "00000000-0000-4000-8000-000000000001"
	token, err := nodeprov.TunnelSessionToken(exitID, c)
	if err != nil || len(token) != 64 {
		t.Fatalf("invalid canonical session token %q: %v", token, err)
	}
	again, err := nodeprov.TunnelSessionToken(exitID, c)
	if err != nil || again != token {
		t.Fatalf("same observation changed token: %s != %s (%v)", again, token, err)
	}
}
