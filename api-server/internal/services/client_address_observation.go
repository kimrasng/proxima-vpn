package services

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// ObservedAddressTransport is one exact authenticated Exit transport whose
// original client address was resolved unambiguously.
type ObservedAddressTransport struct {
	ExitID     string
	Connection nodeprov.TunnelConnection
}

// ClientAddressObservation joins authenticated Exit transports with the
// Entry's pre-NAT address observations. It is a set of network addresses, not
// a set of physical devices. Unknown relay attribution makes it incomplete.
type ClientAddressObservation struct {
	Complete  bool
	Addresses []string
	// SnapshotID fingerprints all fresh Entry and Exit server report identities.
	// Empty means no complete, independently identifiable fleet snapshot.
	SnapshotID string
	// First authenticated transport time per address. A missing/ambiguous
	// attribution never supplies a guessed start for eviction selection.
	Starts map[string]int64
	// Transports maps a client address to the exact Exit transports that
	// resolved to it. Only unambiguously attributed transports are present:
	// a direct peer, or an Entry peer with exactly one relay flow candidate.
	Transports map[string][]ObservedAddressTransport
	// Idle is the smallest TCP idle time, in milliseconds, among the
	// attributed transports of each address: how long since the peer last
	// acknowledged anything. An address is only as stale as its freshest
	// transport. A missing entry means no idle evidence, never "active".
	Idle map[string]int64
}

// exitConnection is an Exit transport tagged with the Exit that reported it.
type exitConnection struct {
	exitID     string
	connection nodeprov.TunnelConnection
}

// JoinClientAddresses requires the caller to supply the known Entry peer
// addresses for this Exit from authoritative topology, and only fresh reports.
// Never infer "direct" merely because a relay flow is missing: an Entry peer
// always requires one unambiguous original source address.
//
// The connections carry no Exit identity here, so Transports is left empty;
// use joinClientAddresses to obtain exact transport ownership.
func JoinClientAddresses(connections []nodeprov.TunnelConnection, entryPeers map[string]bool, reports []nodeprov.RelayFlowReport) (ClientAddressObservation, error) {
	tagged := make([]exitConnection, 0, len(connections))
	for _, connection := range connections {
		tagged = append(tagged, exitConnection{connection: connection})
	}
	out, err := joinClientAddresses(tagged, entryPeers, reports)
	if err != nil {
		return ClientAddressObservation{}, err
	}
	out.Transports = nil
	return out, nil
}

func joinClientAddresses(connections []exitConnection, entryPeers map[string]bool, reports []nodeprov.RelayFlowReport) (ClientAddressObservation, error) {
	type tuple struct {
		peer, exit         string
		peerPort, exitPort int
	}
	flows := make(map[tuple]map[string]bool)
	for _, report := range reports {
		if !report.Known {
			return ClientAddressObservation{Complete: false}, nil
		}
		for _, f := range report.Flows {
			if err := f.Validate(); err != nil {
				return ClientAddressObservation{}, err
			}
			key := tuple{f.PeerIP, f.ExitIP, f.PeerPort, f.ExitPort}
			if flows[key] == nil {
				flows[key] = make(map[string]bool)
			}
			flows[key][f.ClientIP] = true
		}
	}
	addresses := make(map[string]bool)
	out := ClientAddressObservation{
		Complete:   true,
		Addresses:  []string{},
		Starts:     map[string]int64{},
		Transports: map[string][]ObservedAddressTransport{},
		Idle:       map[string]int64{},
	}
	record := func(raw string, tagged exitConnection) {
		// Key every client by its slot identity so that privacy-address rotation
		// within one IPv6 /64 is one client, consistently with admission/release.
		// An address that cannot be keyed cannot be counted, so the snapshot is
		// incomplete rather than silently under-counting.
		address, err := NormalizeClientAddress(raw)
		if err != nil {
			out.Complete = false
			return
		}
		addresses[address] = true
		acceptedAt := tagged.connection.AcceptedAt
		if previous, exists := out.Starts[address]; !exists || acceptedAt < previous {
			out.Starts[address] = acceptedAt
		}
		out.Transports[address] = append(out.Transports[address], ObservedAddressTransport{ExitID: tagged.exitID, Connection: tagged.connection})
		if idle, seen := out.Idle[address]; !seen || tagged.connection.IdleMillis < idle {
			out.Idle[address] = tagged.connection.IdleMillis
		}
	}
	for _, tagged := range connections {
		connection := tagged.connection
		if err := connection.Validate(); err != nil {
			return ClientAddressObservation{}, fmt.Errorf("invalid Exit transport: %w", err)
		}
		peer := netip.MustParseAddr(connection.PeerIP).Unmap().String()
		if !entryPeers[peer] {
			record(peer, tagged)
			continue
		}
		key := tuple{peer, netip.MustParseAddr(connection.LocalIP).Unmap().String(), connection.PeerPort, connection.LocalPort}
		candidates := flows[key]
		if len(candidates) != 1 {
			out.Complete = false
			continue
		}
		for address := range candidates {
			record(address, tagged)
		}
	}
	for address := range addresses {
		out.Addresses = append(out.Addresses, address)
	}
	sort.Strings(out.Addresses)
	return out, nil
}
