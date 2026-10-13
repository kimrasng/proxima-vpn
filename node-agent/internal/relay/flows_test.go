package relay

import (
	"reflect"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestParseFlowsRetainsAmbiguousTupleReuse(t *testing.T) {
	data := []byte(`{"nftables":[{"metainfo":{"version":"1.0.6"}},{"set":{"family":"inet","table":"proxima_relay","name":"client_flows","elem":[{"elem":{"val":{"concat":["10.0.2.1",40000,"10.0.2.2",8443,"10.0.1.3"]},"expires":299}},{"elem":{"val":{"concat":["10.0.2.1",40000,"10.0.2.2",8443,"10.0.1.2"]},"expires":299}},{"elem":{"val":{"concat":["10.0.2.1",40000,"10.0.2.2",8443,"10.0.1.2"]},"expires":299}}]}}]}`)
	got, err := parseFlows(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []nodeprov.RelayFlow{
		{PeerIP: "10.0.2.1", PeerPort: 40000, ExitIP: "10.0.2.2", ExitPort: 8443, ClientIP: "10.0.1.2"},
		{PeerIP: "10.0.2.1", PeerPort: 40000, ExitIP: "10.0.2.2", ExitPort: 8443, ClientIP: "10.0.1.3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseFlowsDistinguishesEmptyFromUnknown(t *testing.T) {
	got, err := parseFlows([]byte(`{"nftables":[{"set":{"family":"inet","table":"proxima_relay","name":"client_flows"}}]}`))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty set: %+v, %v", got, err)
	}
	for _, data := range []string{
		`{"nftables":[]}`,
		`invalid`,
		`{"nftables":[{"set":{"family":"inet","table":"proxima_relay","name":"client_flows","elem":[{"elem":{"val":{"concat":["not-an-ip",12,"10.0.2.2",8443,"10.0.1.2"]}}}]}}]}`,
	} {
		if _, err := parseFlows([]byte(data)); err == nil {
			t.Errorf("accepted invalid response %s", data)
		}
	}
}

func TestRenderObservesTCPFlowsAfterMasqueradeWithoutChangingVerdicts(t *testing.T) {
	for _, protocol := range []nodeprov.Transport{nodeprov.TransportTCP, nodeprov.TransportUDP, nodeprov.TransportTCPUDP} {
		script, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{{EntryPort: 24443, Transport: protocol, ExitIP: "10.0.2.2", ExitPort: 8443}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := protocol != nodeprov.TransportUDP
		if strings.Contains(script, "set client_flows {") != want {
			t.Fatalf("%s: unexpected flow set", protocol)
		}
		if want && !strings.Contains(script, "hook postrouting priority srcnat + 10; policy accept;") {
			t.Fatal("observation must run after source NAT")
		}
		if !strings.Contains(script, "ip daddr 10.0.2.2 masquerade") {
			t.Fatal("observation changed masquerade")
		}
	}
}

func TestFlowsRequiresInstalledPolicyAndPropagatesReadFailure(t *testing.T) {
	m := NewManager()
	if _, err := m.Flows(); err == nil {
		t.Fatal("uninstalled policy reported an empty observation")
	}
	m.applied = "no TCP rules"
	if got, err := m.Flows(); err != nil || len(got) != 0 {
		t.Fatalf("no TCP routes: %+v %v", got, err)
	}
	original := listFlows
	t.Cleanup(func() { listFlows = original })
	m.applied = "set client_flows {"
	listFlows = func() ([]byte, error) { return nil, errRefused }
	if _, err := m.Flows(); err == nil {
		t.Fatal("read failure reported an empty observation")
	}
}
