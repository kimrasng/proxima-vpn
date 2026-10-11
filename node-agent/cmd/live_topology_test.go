package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

func runtimeConfig(devices map[string]string) string {
	outbounds := []string{`{"tag":"direct","protocol":"freedom"}`, `{"tag":"block","protocol":"blackhole"}`}
	rules := []string{`{"type":"field","inboundTag":["api"],"outboundTag":"api"}`}
	for _, id := range []string{"uuid-a", "uuid-b"} {
		pass, ok := devices[id]
		if !ok {
			continue
		}
		outbounds = append(outbounds, `{"tag":"device-egress-`+id+`","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":10086,"users":[{"user":"`+id+`","pass":"`+pass+`"}]}]}}`)
		rules = append(rules, `{"type":"field","user":["`+id+`@proxima"],"outboundTag":"device-egress-`+id+`"}`)
	}
	rules = append(rules, `{"type":"field","inboundTag":["vless-in"],"outboundTag":"block"}`)
	return `{"outbounds":[` + strings.Join(outbounds, ",") + `],"routing":{"rules":[` + strings.Join(rules, ",") + `]}}`
}

type fakeTopologyClient struct {
	calls []string
	rules []xray.RoutingRule
	fail  string
}

func (f *fakeTopologyClient) call(name string) error {
	f.calls = append(f.calls, name)
	if name == f.fail {
		return errors.New("injected")
	}
	return nil
}

func (f *fakeTopologyClient) AddSOCKSOutbound(_ context.Context, o xray.SOCKSOutbound) error {
	return f.call("add " + o.Tag + " " + o.Password)
}

func (f *fakeTopologyClient) RemoveOutbound(_ context.Context, tag string) error {
	return f.call("remove " + tag)
}

func (f *fakeTopologyClient) ReplaceRoutingRules(_ context.Context, rules []xray.RoutingRule) error {
	f.rules = rules
	return f.call("rules")
}

func TestSyncDeviceTopologyAdmitsBeforeRoutingAndRevokesAfter(t *testing.T) {
	one := runtimeConfig(map[string]string{"uuid-a": "pa"})
	two := runtimeConfig(map[string]string{"uuid-a": "pa", "uuid-b": "pb"})
	onlyB := runtimeConfig(map[string]string{"uuid-b": "pb"})

	admit := &fakeTopologyClient{}
	if !syncDeviceTopology(t.Context(), admit, []byte(one), []byte(two)) {
		t.Fatal("admission failed")
	}
	// The outbound must exist before any rule routes to it.
	if want := []string{"add device-egress-uuid-b pb", "rules"}; !reflect.DeepEqual(admit.calls, want) {
		t.Fatalf("admission calls = %q, want %q", admit.calls, want)
	}
	var order []string
	for _, rule := range admit.rules {
		order = append(order, rule.OutboundTag)
	}
	if want := []string{"api", "device-egress-uuid-a", "device-egress-uuid-b", "block"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("rule order = %q, want deny fallback last: %q", order, want)
	}

	revoke := &fakeTopologyClient{}
	if !syncDeviceTopology(t.Context(), revoke, []byte(two), []byte(onlyB)) {
		t.Fatal("revocation failed")
	}
	// Nothing may route to an outbound when it is removed.
	if want := []string{"rules", "remove device-egress-uuid-a"}; !reflect.DeepEqual(revoke.calls, want) {
		t.Fatalf("revocation calls = %q, want %q", revoke.calls, want)
	}

	same := &fakeTopologyClient{}
	if !syncDeviceTopology(t.Context(), same, []byte(two), []byte(two)) || len(same.calls) != 0 {
		t.Fatalf("unchanged topology issued calls: %q", same.calls)
	}
}

func TestSyncDeviceTopologyFallsBackToRestart(t *testing.T) {
	one := runtimeConfig(map[string]string{"uuid-a": "pa"})
	two := runtimeConfig(map[string]string{"uuid-a": "pa", "uuid-b": "pb"})
	for name, tc := range map[string]struct {
		prev, next string
		fail       string
	}{
		"credential changed in place": {one, runtimeConfig(map[string]string{"uuid-a": "rotated"}), ""},
		"unmaterialized credential":   {one, runtimeConfig(map[string]string{"uuid-a": "pa", "uuid-b": deviceEgressPlaceholder}), ""},
		"balancers present":           {one, strings.Replace(two, `"rules":`, `"balancers":[{"tag":"lb","selector":["direct"]}],"rules":`, 1), ""},
		"rule xray cannot be sent":    {one, strings.Replace(two, `"inboundTag":["vless-in"]`, `"inboundTag":["vless-in"],"domain":["example.com"]`, 1), ""},
		"no current runtime config":   {"", two, ""},
		"add outbound rejected":       {one, two, "add device-egress-uuid-b pb"},
		"rule swap rejected":          {one, two, "rules"},
		"remove outbound rejected":    {two, one, "remove device-egress-uuid-b"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeTopologyClient{fail: tc.fail}
			if syncDeviceTopology(t.Context(), client, []byte(tc.prev), []byte(tc.next)) {
				t.Fatal("live edit reported success; the agent would skip the safe restart")
			}
		})
	}
	if syncDeviceTopology(t.Context(), nil, []byte(one), []byte(two)) {
		t.Fatal("nil client reported success")
	}
}
