package nodeprov

import "testing"

func TestAllocateEntryPortHonoursARequestedPort(t *testing.T) {
	got, err := AllocateEntryPort(5993, TransportTCP, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 5993 {
		t.Errorf("got port %d, want the requested 5993", got)
	}
}

func TestAllocateEntryPortFallsBackToTheRelayRange(t *testing.T) {
	relay := RelayRange()

	got, err := AllocateEntryPort(0, TransportTCP, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != relay.Start {
		t.Errorf("got port %d, want the first free port in %v", got, relay)
	}
}

func TestAllocateEntryPortSkipsClaimedPorts(t *testing.T) {
	relay := RelayRange()
	taken := []PortClaim{
		{Port: relay.Start, Transport: TransportTCP},
		{Port: relay.Start + 1, Transport: TransportTCP},
	}

	got, err := AllocateEntryPort(0, TransportTCP, taken)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != relay.Start+2 {
		t.Errorf("got port %d, want %d", got, relay.Start+2)
	}
}

// tcp and udp are separate sockets, so one pool may legitimately front a TCP
// Reality exit and a UDP Hysteria2 exit on the same number.
func TestAllocateEntryPortLetsTCPAndUDPShareANumber(t *testing.T) {
	taken := []PortClaim{{Port: 5993, Transport: TransportTCP}}

	got, err := AllocateEntryPort(5993, TransportUDP, taken)
	if err != nil {
		t.Fatalf("udp was refused a port held only by tcp: %v", err)
	}
	if got != 5993 {
		t.Errorf("got port %d, want 5993", got)
	}
}

// tcp_udp claims both sockets, so it must collide with either single protocol in
// both directions - this is the overlap a partial unique index cannot express,
// which is why the allocator has to reject it.
func TestAllocateEntryPortRejectsTCPUDPOverlap(t *testing.T) {
	for name, tc := range map[string]struct {
		taken []PortClaim
		want  Transport
	}{
		"tcp_udp over existing tcp": {
			taken: []PortClaim{{Port: 5993, Transport: TransportTCP}},
			want:  TransportTCPUDP,
		},
		"tcp_udp over existing udp": {
			taken: []PortClaim{{Port: 5993, Transport: TransportUDP}},
			want:  TransportTCPUDP,
		},
		"tcp over existing tcp_udp": {
			taken: []PortClaim{{Port: 5993, Transport: TransportTCPUDP}},
			want:  TransportTCP,
		},
		"udp over existing tcp_udp": {
			taken: []PortClaim{{Port: 5993, Transport: TransportTCPUDP}},
			want:  TransportUDP,
		},
		"tcp_udp over existing tcp_udp": {
			taken: []PortClaim{{Port: 5993, Transport: TransportTCPUDP}},
			want:  TransportTCPUDP,
		},
	} {
		if _, err := AllocateEntryPort(5993, tc.want, tc.taken); err == nil {
			t.Errorf("%s: overlap was accepted", name)
		}
	}
}

func TestAllocateEntryPortAutoSkipsTCPUDPOverlap(t *testing.T) {
	relay := RelayRange()
	taken := []PortClaim{{Port: relay.Start, Transport: TransportTCPUDP}}

	got, err := AllocateEntryPort(0, TransportUDP, taken)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == relay.Start {
		t.Errorf("allocated %d, which a tcp_udp chain already claims", got)
	}
}

// A speed-tier inbound listens in that range on every node, so a chain placed
// there would have its traffic answered by Xray instead of being forwarded.
func TestAllocateEntryPortRejectsTheSpeedTierRange(t *testing.T) {
	tier := TierRange()

	if _, err := AllocateEntryPort(tier.Start, TransportTCP, nil); err == nil {
		t.Errorf("port %d inside the tier range %v was accepted", tier.Start, tier)
	}
	if _, err := AllocateEntryPort(tier.End, TransportTCP, nil); err == nil {
		t.Errorf("port %d inside the tier range %v was accepted", tier.End, tier)
	}
}

func TestAllocateEntryPortRejectsBadInput(t *testing.T) {
	if _, err := AllocateEntryPort(70000, TransportTCP, nil); err == nil {
		t.Error("an out-of-range port was accepted")
	}
	if _, err := AllocateEntryPort(0, "sctp", nil); err == nil {
		t.Error("an unknown transport was accepted")
	}
}

func TestAllocateEntryPortReportsExhaustion(t *testing.T) {
	relay := RelayRange()
	taken := make([]PortClaim, 0, MaxRelayChains)
	for port := relay.Start; port <= relay.End; port++ {
		taken = append(taken, PortClaim{Port: port, Transport: TransportTCPUDP})
	}

	if _, err := AllocateEntryPort(0, TransportTCP, taken); err == nil {
		t.Error("a full relay range still yielded a port")
	}
}

// The agent skips a reload when the rendered ruleset is unchanged, which only
// holds if an unchanged rule set always sorts the same way.
func TestSortRulesIsDeterministic(t *testing.T) {
	rules := []RelayRule{
		{EntryPort: 5993, Transport: TransportUDP, ExitIP: "203.0.113.11", ExitPort: 8443},
		{EntryPort: 5223, Transport: TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
		{EntryPort: 5993, Transport: TransportTCP, ExitIP: "203.0.113.12", ExitPort: 443},
	}
	SortRules(rules)

	want := []struct {
		port      int
		transport Transport
	}{
		{5223, TransportTCP},
		{5993, TransportTCP},
		{5993, TransportUDP},
	}
	for i, w := range want {
		if rules[i].EntryPort != w.port || rules[i].Transport != w.transport {
			t.Fatalf("rule %d is %d/%s, want %d/%s",
				i, rules[i].EntryPort, rules[i].Transport, w.port, w.transport)
		}
	}
}

func TestRelayRuleValidateRejectsIncompleteRules(t *testing.T) {
	for name, rule := range map[string]RelayRule{
		"no exit ip":        {EntryPort: 5993, Transport: TransportTCP, ExitPort: 443},
		"zero entry port":   {Transport: TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
		"zero exit port":    {EntryPort: 5993, Transport: TransportTCP, ExitIP: "203.0.113.10"},
		"unknown transport": {EntryPort: 5993, Transport: "sctp", ExitIP: "203.0.113.10", ExitPort: 443},
	} {
		if err := rule.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	valid := RelayRule{EntryPort: 5993, Transport: TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443}
	if err := valid.Validate(); err != nil {
		t.Errorf("a complete rule was rejected: %v", err)
	}
}

func TestExitRuleValidateAcceptsEmptyRelayPool(t *testing.T) {
	rule := ExitRule{ExitPort: 443, Transport: TransportTCP, RelayIPs: []string{}}

	if err := rule.Validate(); err != nil {
		t.Errorf("an empty relay pool was rejected: %v", err)
	}
}

func TestExitRuleValidateRejectsInvalidRelaySourceIP(t *testing.T) {
	for name, relayIP := range map[string]string{
		"malformed address": "relay.example.test",
		"ipv6 address":      "2001:db8::10",
	} {
		t.Run(name, func(t *testing.T) {
			rule := ExitRule{ExitPort: 443, Transport: TransportTCP, RelayIPs: []string{relayIP}}

			if err := rule.Validate(); err == nil {
				t.Errorf("relay source IP %q was accepted", relayIP)
			}
		})
	}
}

func TestSortExitRulesOrdersPortBeforeTransport(t *testing.T) {
	rules := []ExitRule{
		{ExitPort: 443, Transport: TransportUDP, RelayIPs: []string{"203.0.113.20"}},
		{ExitPort: 8443, Transport: TransportTCP, RelayIPs: []string{}},
		{ExitPort: 443, Transport: TransportTCP, RelayIPs: []string{"203.0.113.10"}},
	}

	SortExitRules(rules)

	want := []struct {
		port      int
		transport Transport
	}{
		{443, TransportTCP},
		{443, TransportUDP},
		{8443, TransportTCP},
	}
	for index, expected := range want {
		if rules[index].ExitPort != expected.port || rules[index].Transport != expected.transport {
			t.Fatalf("rule %d is %d/%s, want %d/%s",
				index, rules[index].ExitPort, rules[index].Transport, expected.port, expected.transport)
		}
	}
}
