package services

import (
	"fmt"
	"net/netip"
)

// clientAddressIPv6PrefixBits is the IPv6 granularity at which one client is
// recognised. SLAAC privacy extensions rotate a host's interface identifier
// (the low 64 bits) every few hours or on every new connection, so keying a
// slot by the exact address would make one device hold several slots and trip
// a cap it never exceeded. Hosts on one /64 are one LAN, matching how several
// devices behind one IPv4 NAT already count as a single client.
const clientAddressIPv6PrefixBits = 64

// NormalizeClientAddress returns the canonical slot key for a client source
// address. It is the single definition shared by observation, admission and
// release, so all three always agree on what "the same client address" means.
//
//   - IPv4 and IPv4-mapped IPv6 keep their exact address.
//   - IPv6 collapses to its /64 network address.
//
// The key is itself a valid address, and normalizing a key returns it
// unchanged, so already-normalized values may be passed through again.
// Zoned, unspecified and multicast addresses are rejected, as is an IPv6
// address inside ::/64 (loopback and other reserved space), which would
// otherwise collapse onto the unspecified address.
//
// Errors deliberately never include the address.
func NormalizeClientAddress(raw string) (string, error) {
	ip, err := netip.ParseAddr(raw)
	if err != nil || ip.Zone() != "" {
		return "", fmt.Errorf("invalid client address")
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsMulticast() {
		return "", fmt.Errorf("invalid client address")
	}
	if ip.Is4() {
		return ip.String(), nil
	}
	prefix, err := ip.Prefix(clientAddressIPv6PrefixBits)
	if err != nil {
		return "", fmt.Errorf("invalid client address")
	}
	network := prefix.Addr()
	if network.IsUnspecified() {
		return "", fmt.Errorf("invalid client address")
	}
	return network.String(), nil
}
