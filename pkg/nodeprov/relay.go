package nodeprov

import (
	"fmt"
	"net/netip"
	"sort"
)

// Transport is the L4 protocol a relay chain forwards. It exists because the
// exit's protocol decides it: Reality is TCP, Hysteria2 and WireGuard are UDP.
// A relay never looks inside the packet, so this is the only thing it needs to
// know about the protocol it is carrying.
type Transport string

const (
	TransportTCP    Transport = "tcp"
	TransportUDP    Transport = "udp"
	TransportTCPUDP Transport = "tcp_udp"
)

func (t Transport) Valid() bool {
	switch t {
	case TransportTCP, TransportUDP, TransportTCPUDP:
		return true
	}
	return false
}

// Covers reports whether t forwards the given single protocol.
func (t Transport) Covers(other Transport) bool {
	if t == TransportTCPUDP {
		return other == TransportTCP || other == TransportUDP || other == TransportTCPUDP
	}
	return t == other
}

func Transports() []Transport {
	return []Transport{TransportTCP, TransportUDP, TransportTCPUDP}
}

// PortClaim is one entry port already taken on the fleet, as read from
// node_chains. The allocator needs the transport too, because tcp and udp may
// share a number while tcp_udp claims both.
type PortClaim struct {
	Port      int
	Transport Transport
}

// conflictsWith reports whether granting want would collide with this claim.
func (c PortClaim) conflictsWith(port int, want Transport) bool {
	return c.Port == port && (c.Transport.Covers(want) || want.Covers(c.Transport))
}

// AllocateEntryPort picks the entry port a new chain should listen on.
//
// A requested port is honoured when free, so an operator can place a chain on a
// number that blends in - the auto range is contiguous, and a host with several
// hundred ports open inside one narrow window is itself a signal worth avoiding.
// Passing 0 falls back to the first free port in RelayRange.
//
// taken must list every entry port already claimed fleet-wide, not just those on
// one relay: a chain's rule is replicated to every relay in its pool, so a port
// free on one relay but taken on another would make replication order decide
// whether the chain works.
func AllocateEntryPort(requested int, want Transport, taken []PortClaim) (int, error) {
	if !want.Valid() {
		return 0, fmt.Errorf("unknown transport %q", want)
	}

	if requested != 0 {
		if err := ValidatePort(requested); err != nil {
			return 0, err
		}
		if inSpec(requested, TierRange()) {
			return 0, fmt.Errorf("port %d is inside the speed-tier range %v", requested, TierRange())
		}
		for _, c := range taken {
			if c.conflictsWith(requested, want) {
				return 0, fmt.Errorf("port %d is already claimed for %s", requested, c.Transport)
			}
		}
		return requested, nil
	}

	claimed := make(map[int][]Transport, len(taken))
	for _, c := range taken {
		claimed[c.Port] = append(claimed[c.Port], c.Transport)
	}

	relay := RelayRange()
	for port := relay.Start; port <= relay.End; port++ {
		free := true
		for _, t := range claimed[port] {
			if t.Covers(want) || want.Covers(t) {
				free = false
				break
			}
		}
		if free {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free entry port in %v for %s", relay, want)
}

func inSpec(port int, s PortSpec) bool {
	return port >= s.Start && port <= s.End
}

// RelayRule is one forwarding instruction a relay applies: traffic arriving on
// EntryPort is rewritten to ExitIP:ExitPort. It carries no credentials because
// the relay never terminates the tunnel - which is what lets the same rule carry
// Reality, Hysteria2 or WireGuard without knowing which it is.
type RelayRule struct {
	EntryPort int       `json:"entry_port"`
	Transport Transport `json:"transport"`
	ExitIP    string    `json:"exit_ip"`
	ExitPort  int       `json:"exit_port"`
}

func (r RelayRule) Validate() error {
	if err := ValidatePort(r.EntryPort); err != nil {
		return fmt.Errorf("entry port: %w", err)
	}
	if err := ValidatePort(r.ExitPort); err != nil {
		return fmt.Errorf("exit port: %w", err)
	}
	if !r.Transport.Valid() {
		return fmt.Errorf("unknown transport %q", r.Transport)
	}
	if r.ExitIP == "" {
		return fmt.Errorf("exit ip is required")
	}
	return nil
}

// SortRules orders rules so an unchanged rule set always renders to identical
// text. Without that the agent cannot tell a real change from a reordered
// response, and would reload nftables on every poll.
func SortRules(rules []RelayRule) {
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].EntryPort != rules[j].EntryPort {
			return rules[i].EntryPort < rules[j].EntryPort
		}
		return rules[i].Transport < rules[j].Transport
	})
}

// ExitRule is one admission instruction an exit applies to a tunnel listener.
// RelayIPs is intentionally allowed to be empty: the listener then remains
// protected while its relay pool has no eligible members.
type ExitRule struct {
	ExitPort  int       `json:"exit_port"`
	Transport Transport `json:"transport"`
	RelayIPs  []string  `json:"relay_ips"`
}

func (r ExitRule) Validate() error {
	if err := ValidatePort(r.ExitPort); err != nil {
		return fmt.Errorf("exit port: %w", err)
	}
	if !r.Transport.Valid() {
		return fmt.Errorf("unknown transport %q", r.Transport)
	}
	for index, relayIP := range r.RelayIPs {
		address, err := netip.ParseAddr(relayIP)
		if err != nil || !address.Is4() {
			return fmt.Errorf("relay ip %d %q is not an IPv4 address", index, relayIP)
		}
	}
	return nil
}

func SortExitRules(rules []ExitRule) {
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ExitPort != rules[j].ExitPort {
			return rules[i].ExitPort < rules[j].ExitPort
		}
		return rules[i].Transport < rules[j].Transport
	})
}
