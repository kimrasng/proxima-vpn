package relay

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// No unit test may reach the host's nft; tests that care about counters
// replace resetCounters themselves.
func TestMain(m *testing.M) {
	resetCounters = func() ([]byte, error) { return []byte(`{"nftables": []}`), nil }
	os.Exit(m.Run())
}

func counterJSON(counters map[string]int64) []byte {
	parts := []string{`{"metainfo": {"version": "1.0.6"}}`}
	for name, bytes := range counters {
		parts = append(parts, fmt.Sprintf(`{"counter": {"family": "inet", "name": %q, "table": "proxima_relay", "handle": 1, "packets": 1, "bytes": %d}}`, name, bytes))
	}
	return []byte(`{"nftables": [` + strings.Join(parts, ", ") + `]}`)
}

// Every forwarded route gets an up and a down counter per protocol, looked up
// by the conntrack original destination port so replies land on the route the
// client dialled, and the counting rules sit before the accept verdicts.
func TestRenderCountsEachRouteProtocolAndDirection(t *testing.T) {
	script, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 24444, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.11", ExitPort: 8443},
		{EntryPort: 24443, Transport: nodeprov.TransportTCPUDP, ExitIP: "203.0.113.10", ExitPort: 443},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"counter route_up_tcp_24443 { }", "counter route_dn_tcp_24443 { }",
		"counter route_up_tcp_24444 { }", "counter route_dn_tcp_24444 { }",
		"counter route_up_udp_24443 { }", "counter route_dn_udp_24443 { }",
		`ct status dnat meta l4proto tcp ct direction original counter name ct original proto-dst map { 24443 : "route_up_tcp_24443", 24444 : "route_up_tcp_24444" }`,
		`ct status dnat meta l4proto tcp ct direction reply counter name ct original proto-dst map { 24443 : "route_dn_tcp_24443", 24444 : "route_dn_tcp_24444" }`,
		`ct status dnat meta l4proto udp ct direction original counter name ct original proto-dst map { 24443 : "route_up_udp_24443" }`,
		`ct status dnat meta l4proto udp ct direction reply counter name ct original proto-dst map { 24443 : "route_dn_udp_24443" }`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "route_up_udp_24444") {
		t.Error("a TCP-only route got a UDP counter")
	}
	forward := script[strings.Index(script, "chain forward"):]
	if strings.Index(forward, "counter name") > strings.Index(forward, "ct state established,related accept") {
		t.Error("counting rules must run before the accept verdicts")
	}
}

func TestRenderExitOnlyPolicyHasNoRouteCounters(t *testing.T) {
	script, err := Render(nodeprov.RoleExit, nil, []nodeprov.ExitRule{{ExitPort: 443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "counter route_") {
		t.Errorf("an Exit forwards nothing and must not declare route counters:\n%s", script)
	}
}

// Reading counters must not lose bytes when the table is replaced (which
// zeroes it) and must hand back a failed delivery for the next report.
func TestDrainTrafficCarriesBytesAcrossTableReplacementAndFailedDelivery(t *testing.T) {
	originalScript, originalCmd, originalReset := runScript, runCmd, resetCounters
	t.Cleanup(func() { runScript, runCmd, resetCounters = originalScript, originalCmd, originalReset })
	runScript = func(string) error { return nil }
	runCmd = func(string, ...string) error { return nil }

	// Simulate the kernel: counters accumulate until reset.
	kernel := map[string]int64{}
	resets := 0
	resetCounters = func() ([]byte, error) {
		resets++
		out := counterJSON(kernel)
		kernel = map[string]int64{}
		return out, nil
	}

	ruleA := nodeprov.RelayRule{EntryPort: 24443, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443}
	ruleB := nodeprov.RelayRule{EntryPort: 24444, Transport: nodeprov.TransportUDP, ExitIP: "203.0.113.11", ExitPort: 8443}
	m := NewManager()
	if err := m.Apply(nodeprov.RoleRelay, []nodeprov.RelayRule{ruleA}, nil); err != nil {
		t.Fatal(err)
	}
	kernel["route_up_tcp_24443"] = 1000
	kernel["route_dn_tcp_24443"] = 5000

	// A policy change replaces the table; the bytes counted so far survive.
	if err := m.Apply(nodeprov.RoleRelay, []nodeprov.RelayRule{ruleA, ruleB}, nil); err != nil {
		t.Fatal(err)
	}
	kernel["route_up_tcp_24443"] = 10
	kernel["route_up_udp_24444"] = 7
	kernel["proxima_unrelated"] = 99

	got, err := m.DrainTraffic()
	if err != nil {
		t.Fatal(err)
	}
	want := []nodeprov.RouteTraffic{
		{EntryPort: 24443, Transport: nodeprov.TransportTCP, UpBytes: 1010, DownBytes: 5000},
		{EntryPort: 24444, Transport: nodeprov.TransportUDP, UpBytes: 7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first drain = %+v, want %+v", got, want)
	}

	// Delivery failed: the report comes back and merges with new traffic.
	m.ReturnTraffic(got)
	kernel["route_dn_udp_24444"] = 3
	got, err = m.DrainTraffic()
	if err != nil {
		t.Fatal(err)
	}
	want[1].DownBytes = 3
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drain after a failed delivery = %+v, want %+v", got, want)
	}

	// Nothing new: an empty report, not a repeat.
	if got, _ := m.DrainTraffic(); len(got) != 0 {
		t.Fatalf("a drained interval was reported twice: %+v", got)
	}

	// An unchanged poll must not touch the counters.
	before := resets
	if err := m.Apply(nodeprov.RoleRelay, []nodeprov.RelayRule{ruleB, ruleA}, nil); err != nil {
		t.Fatal(err)
	}
	if resets != before {
		t.Error("an unchanged policy reset the route counters")
	}
}

// A counter read failure is a lost diagnostic interval, never a policy
// failure: forwarding must still be applied and the status stay clean.
func TestCounterReadFailureDoesNotBlockPolicy(t *testing.T) {
	originalScript, originalCmd, originalReset := runScript, runCmd, resetCounters
	t.Cleanup(func() { runScript, runCmd, resetCounters = originalScript, originalCmd, originalReset })
	scripts := 0
	runScript = func(string) error { scripts++; return nil }
	runCmd = func(string, ...string) error { return nil }
	resetCounters = func() ([]byte, error) { return nil, fmt.Errorf("nft reset counters: no such table") }

	m := NewManager()
	rules := []nodeprov.RelayRule{{EntryPort: 24443, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443}}
	if err := m.Apply(nodeprov.RoleRelay, rules, nil); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := m.Apply(nodeprov.RoleRelay, append(rules, nodeprov.RelayRule{EntryPort: 24444, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.11", ExitPort: 443}), nil); err != nil {
		t.Fatalf("replacement apply: %v", err)
	}
	if scripts != 2 || m.Status().Error != "" {
		t.Fatalf("scripts=%d status=%+v; counter failures must not affect policy", scripts, m.Status())
	}
	if _, err := m.DrainTraffic(); err == nil {
		t.Error("DrainTraffic should surface the read failure to its caller")
	}
}

func TestParseRouteCountersIgnoresForeignAndMalformedObjects(t *testing.T) {
	got, err := parseRouteCounters(counterJSON(map[string]int64{
		"route_up_tcp_443":   5,
		"route_dn_tcp_443":   6,
		"route_up_sctp_443":  1,
		"route_up_tcp_70000": 1,
		"route_xx_tcp_443":   1,
		"other_counter":      1,
		"route_up_udp_53":    0,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[routeCounterKey]routeCounts{{443, nodeprov.TransportTCP}: {5, 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
	if _, err := parseRouteCounters([]byte("not json")); err == nil {
		t.Error("malformed nft output must be an error")
	}
}
