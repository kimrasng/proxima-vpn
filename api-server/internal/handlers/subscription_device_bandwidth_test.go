package handlers

import "testing"

func TestLimitedRelayRequiresDeviceLimiterAcknowledgmentAndVlessListener(t *testing.T) {
	node := relayedTo("entry.example", 24443, 443)
	node.DeviceBandwidthReady = true
	node.RealityReady = true
	node.GeneratedRealityPorts = []int{443, 20050}
	infos := buildNodeInfoList([]subscriptionNode{node}, "device", 50, nil, nil)
	if len(infos) != 1 || infos[0].Port != 24443 || infos[0].IP != "entry.example" || infos[0].Protocol != "vless_reality" {
		t.Fatalf("limited relay not projected safely: %+v", infos)
	}
	node.DeviceBandwidthReady = false
	if got := buildNodeInfoList([]subscriptionNode{node}, "device", 50, nil, nil); len(got) != 0 {
		t.Fatal("legacy shaping advertised as device limiter")
	}
	node.DeviceBandwidthReady = true
	node.ChainExitPort = 51820
	if got := buildNodeInfoList([]subscriptionNode{node}, "device", 50, nil, nil); len(got) != 0 {
		t.Fatal("non-VLESS relay bypass advertised")
	}
}

func TestLimitedDirectDoesNotAdvertiseSharedTierOnly(t *testing.T) {
	node := exitNode()
	node.RealityReady = true
	node.GeneratedRealityPorts = []int{443, 20050}
	if got := buildNodeInfoList([]subscriptionNode{node}, "device", 50, nil, nil); len(got) != 0 {
		t.Fatal("missing per-device ack advertised")
	}
	node.DeviceBandwidthReady = true
	got := buildNodeInfoList([]subscriptionNode{node}, "device", 50, nil, nil)
	if len(got) != 1 || got[0].Port != 20050 || got[0].Protocol != "vless_reality" {
		t.Fatalf("unexpected controlled direct endpoint %+v", got)
	}
}

// A host that exposes a single external port pins its speed-limited endpoint
// there. The tier listener (config generation) and the published endpoint must
// agree, so the subscription follows tier_port instead of PortBase+Mbps.
func TestLimitedDirectUsesPinnedTierPort(t *testing.T) {
	node := exitNode()
	node.DeviceBandwidthReady = true
	node.TierPort = ptrInt(41538)
	node.GeneratedRealityPorts = []int{443, 41538}
	got := buildNodeInfoList([]subscriptionNode{node}, "device", 50, nil, nil)
	if len(got) != 1 || got[0].Port != 41538 || got[0].Protocol != "vless_reality" {
		t.Fatalf("limited endpoint must use the pinned tier port, got %+v", got)
	}
	// A pinned port the node has not applied yet must be withheld, not guessed.
	node.GeneratedRealityPorts = []int{443, 20050}
	if got := buildNodeInfoList([]subscriptionNode{node}, "device", 50, nil, nil); len(got) != 0 {
		t.Fatalf("an unapplied tier port was advertised: %+v", got)
	}
	// Unlimited clients keep the node's main port.
	node.GeneratedRealityPorts = []int{443, 41538}
	if got := buildNodeInfoList([]subscriptionNode{node}, "device", 0, nil, nil); len(got) == 0 || got[0].Port != 443 {
		t.Fatalf("unlimited clients must ignore tier_port, got %+v", got)
	}
}
