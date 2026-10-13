package services

import (
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"testing"
)

func TestRelayAddressRouteUsesOriginalCohortAndObservedPeer(t *testing.T) {
	c := nodeprov.TunnelConnection{UUID: "00000000-0000-4000-8000-000000000001", PeerIP: "10.0.2.1", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 1}
	f := nodeprov.RelayFlow{PeerIP: c.PeerIP, PeerPort: c.PeerPort, ExitIP: c.LocalIP, ExitPort: c.LocalPort, ClientIP: "203.0.113.2"}
	got, err := ProposeRelayAddressRoutes([]nodeprov.TunnelConnection{c, c}, []nodeprov.RelayFlow{f}, map[string]bool{f.ClientIP: true})
	if err != nil || len(got) != 1 {
		t.Fatalf("routes=%+v err=%v", got, err)
	}
	tag, _, _ := devicebandwidth.AddressOutboundTag(c.UUID, f.ClientIP)
	if got[0].OutboundTag != tag || got[0].PeerCIDR != "10.0.2.1/32" || got[0].PeerPort != 40000 {
		t.Fatalf("route=%+v", got[0])
	}
	denied, err := ProposeRelayAddressRoutes([]nodeprov.TunnelConnection{c}, []nodeprov.RelayFlow{f}, nil)
	if err != nil || len(denied) != 0 {
		t.Fatal("denied source received route")
	}
	other := f
	other.ClientIP = "203.0.113.3"
	for _, flows := range [][]nodeprov.RelayFlow{nil, {f, other}} {
		if _, err := ProposeRelayAddressRoutes([]nodeprov.TunnelConnection{c}, flows, map[string]bool{f.ClientIP: true}); err == nil {
			t.Fatal("missing/ambiguous mapping accepted")
		}
	}
}
