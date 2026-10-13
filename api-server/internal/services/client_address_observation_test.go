package services

import (
	"reflect"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestJoinClientAddressesDoesNotCountEntryAsClient(t *testing.T) {
	c := nodeprov.TunnelConnection{UUID: "00000000-0000-4000-8000-000000000001", PeerIP: "10.0.2.1", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 1}
	f := nodeprov.RelayFlow{PeerIP: c.PeerIP, PeerPort: c.PeerPort, ExitIP: c.LocalIP, ExitPort: c.LocalPort, ClientIP: "203.0.113.2"}
	entries := map[string]bool{c.PeerIP: true}
	for _, reports := range [][]nodeprov.RelayFlowReport{
		nil,
		{{Known: false}},
		{{Known: true, Flows: []nodeprov.RelayFlow{}}},
	} {
		got, err := JoinClientAddresses([]nodeprov.TunnelConnection{c}, entries, reports)
		if err != nil || got.Complete {
			t.Fatalf("missing relay attribution accepted: %+v %v", got, err)
		}
	}
	got, err := JoinClientAddresses([]nodeprov.TunnelConnection{c, c}, entries, []nodeprov.RelayFlowReport{{Known: true, Flows: []nodeprov.RelayFlow{f}}})
	if err != nil || !got.Complete || !reflect.DeepEqual(got.Addresses, []string{f.ClientIP}) {
		t.Fatalf("attributed: %+v %v", got, err)
	}
	other := f
	other.ClientIP = "203.0.113.3"
	got, err = JoinClientAddresses([]nodeprov.TunnelConnection{c}, entries, []nodeprov.RelayFlowReport{{Known: true, Flows: []nodeprov.RelayFlow{f, other}}})
	if err != nil || got.Complete {
		t.Fatalf("tuple reuse guessed source: %+v %v", got, err)
	}
}

func TestJoinClientAddressesDeduplicatesDirectAndRelayedAddress(t *testing.T) {
	c := nodeprov.TunnelConnection{UUID: "00000000-0000-4000-8000-000000000001", PeerIP: "10.0.2.1", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 1}
	f := nodeprov.RelayFlow{PeerIP: c.PeerIP, PeerPort: c.PeerPort, ExitIP: c.LocalIP, ExitPort: c.LocalPort, ClientIP: "203.0.113.2"}
	direct := c
	direct.PeerIP = f.ClientIP
	direct.PeerPort = 40001
	got, err := JoinClientAddresses([]nodeprov.TunnelConnection{c, direct}, map[string]bool{c.PeerIP: true}, []nodeprov.RelayFlowReport{{Known: true, Flows: []nodeprov.RelayFlow{f}}})
	if err != nil || !got.Complete || !reflect.DeepEqual(got.Addresses, []string{f.ClientIP}) {
		t.Fatalf("dedup: %+v %v", got, err)
	}
}
