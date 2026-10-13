package services

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// RelayAddressRoute is a proposed source/port qualified rule for one observed
// relay transport. Its local credential cohort follows the original client
// address, while Xray matches the masqueraded peer. This proposal applies only
// to subsequent dispatches; it cannot migrate an existing shared egress flow.
type RelayAddressRoute struct {
	UUID        string
	ClientIP    string
	OutboundTag string
	PeerCIDR    string
	PeerPort    uint16
}

// ProposeRelayAddressRoutes never converts a missing/ambiguous NAT mapping
// into a direct source. Caller must filter account credentials, fresh reports
// and allowed original addresses before using these proposals. Ports are
// connection locators, not stable device identities.
func ProposeRelayAddressRoutes(connections []nodeprov.TunnelConnection, flows []nodeprov.RelayFlow, allowed map[string]bool) ([]RelayAddressRoute, error) {
	type tuple struct {
		peer, exit         string
		peerPort, exitPort int
	}
	indexed := map[tuple]map[string]bool{}
	for _, f := range flows {
		if err := f.Validate(); err != nil {
			return nil, err
		}
		key := tuple{f.PeerIP, f.ExitIP, f.PeerPort, f.ExitPort}
		if indexed[key] == nil {
			indexed[key] = map[string]bool{}
		}
		indexed[key][f.ClientIP] = true
	}
	out := []RelayAddressRoute{}
	seen := map[RelayAddressRoute]bool{}
	for _, c := range connections {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		candidates := indexed[tuple{c.PeerIP, c.LocalIP, c.PeerPort, c.LocalPort}]
		if len(candidates) != 1 {
			return nil, fmt.Errorf("relay source attribution missing or ambiguous")
		}
		for client := range candidates {
			if !allowed[client] {
				continue
			}
			tag, _, err := devicebandwidth.AddressOutboundTag(c.UUID, client)
			if err != nil {
				return nil, err
			}
			peer := netip.MustParseAddr(c.PeerIP).Unmap()
			route := RelayAddressRoute{UUID: c.UUID, ClientIP: client, OutboundTag: tag, PeerCIDR: netip.PrefixFrom(peer, peer.BitLen()).String(), PeerPort: uint16(c.PeerPort)}
			if !seen[route] {
				out = append(out, route)
				seen[route] = true
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.UUID != b.UUID {
			return a.UUID < b.UUID
		}
		if a.PeerCIDR != b.PeerCIDR {
			return a.PeerCIDR < b.PeerCIDR
		}
		return a.PeerPort < b.PeerPort
	})
	return out, nil
}
