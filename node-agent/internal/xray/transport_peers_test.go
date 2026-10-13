package xray

import "testing"

func TestTransportPeersEmptyAndInvalidPorts(t *testing.T) {
	for _, ports := range [][]int{nil, {}} {
		peers, err := TransportPeers(ports)
		if err != nil || peers == nil || len(peers) != 0 {
			t.Fatalf("empty query: peers=%v err=%v", peers, err)
		}
	}
	for _, ports := range [][]int{{0}, {-1}, {65536}, {443, 0}} {
		if _, err := TransportPeers(ports); err == nil {
			t.Fatalf("accepted invalid ports %v", ports)
		}
	}
	set, err := transportPortSet([]int{1, 443, 65535, 443})
	if err != nil || len(set) != 3 || !set[1] || !set[443] || !set[65535] {
		t.Fatalf("unexpected port set: %v %v", set, err)
	}
}
