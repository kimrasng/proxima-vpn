package xray

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveDeviceTopologyAgainstRealXray drives a running Xray through the
// hand-encoded AddOutbound / AddRule / RemoveOutbound calls. It is skipped
// unless PROXIMA_XRAY_API names that Xray's API (for example
// 127.0.0.1:18571); the config it expects is a single device-egress outbound
// plus the api, device and block rules.
func TestLiveDeviceTopologyAgainstRealXray(t *testing.T) {
	addr := os.Getenv("PROXIMA_XRAY_API")
	if addr == "" {
		t.Skip("PROXIMA_XRAY_API not set")
	}
	client, err := NewStatsClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const (
		a = "8f1c7e0a-1b2c-4d3e-9f40-112233445566"
		b = "0b6a2f55-7c1d-4e8f-a012-aabbccddeeff"
	)
	outB := SOCKSOutbound{Tag: "device-egress-" + b, Address: "127.0.0.1", Port: 10086, Username: b, Password: "secret-b"}
	if err := client.AddSOCKSOutbound(ctx, outB); err != nil {
		t.Fatalf("add outbound: %v", err)
	}
	rules := []RoutingRule{
		{OutboundTag: "api", InboundTag: []string{"api"}},
		{OutboundTag: "device-egress-" + a, UserEmail: []string{a + "@proxima"}},
		{OutboundTag: "device-egress-" + b, UserEmail: []string{b + "@proxima"}},
		{OutboundTag: "block", InboundTag: []string{"vless-in"}},
	}
	if err := client.ReplaceRoutingRules(ctx, rules); err != nil {
		t.Fatalf("replace rules: %v", err)
	}
	// Removing device a: rules first, then its outbound.
	if err := client.ReplaceRoutingRules(ctx, append(rules[:1:1], rules[2:]...)); err != nil {
		t.Fatalf("replace rules without a: %v", err)
	}
	if err := client.RemoveOutbound(ctx, "device-egress-"+a); err != nil {
		t.Fatalf("remove outbound: %v", err)
	}
}
