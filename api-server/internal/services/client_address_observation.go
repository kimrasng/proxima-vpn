package services

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// ClientAddressObservation joins authenticated Exit transports with the
// Entry's pre-NAT address observations. It is a set of network addresses, not
// a set of physical devices. Unknown relay attribution makes it incomplete.
type ClientAddressObservation struct {
	Complete  bool
	Addresses []string
}

// JoinClientAddresses requires the caller to supply the known Entry peer
// addresses for this Exit from authoritative topology, and only fresh reports.
// Never infer "direct" merely because a relay flow is missing: an Entry peer
// always requires one unambiguous original source address.
func JoinClientAddresses(connections []nodeprov.TunnelConnection, entryPeers map[string]bool, reports []nodeprov.RelayFlowReport) (ClientAddressObservation, error) {
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
	out := ClientAddressObservation{Complete: true, Addresses: []string{}}
	for _, connection := range connections {
		if err := connection.Validate(); err != nil {
			return ClientAddressObservation{}, fmt.Errorf("invalid Exit transport: %w", err)
		}
		peer := netip.MustParseAddr(connection.PeerIP).Unmap().String()
		if !entryPeers[peer] {
			addresses[peer] = true
			continue
		}
		key := tuple{peer, netip.MustParseAddr(connection.LocalIP).Unmap().String(), connection.PeerPort, connection.LocalPort}
		candidates := flows[key]
		if len(candidates) != 1 {
			out.Complete = false
			continue
		}
		for address := range candidates {
			addresses[address] = true
		}
	}
	for address := range addresses {
		out.Addresses = append(out.Addresses, address)
	}
	sort.Strings(out.Addresses)
	return out, nil
}
