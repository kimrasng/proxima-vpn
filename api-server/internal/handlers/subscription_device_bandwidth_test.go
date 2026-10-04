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
