package xray

import (
	"fmt"
	"net/netip"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// CloseObservedTransport closes only a connection whose currently indexed
// authenticated ownership still matches the command. Peer reuse, a restart,
// or ambiguous ownership fails closed: no unrelated socket is destroyed.
func (r *XrayRunner) CloseObservedTransport(command nodeprov.TransportClosure) error {
	connection := command.Connection
	if err := connection.Validate(); err != nil {
		return err
	}
	peer := netip.AddrPortFrom(netip.MustParseAddr(connection.PeerIP).Unmap(), uint16(connection.PeerPort))
	local := netip.AddrPortFrom(netip.MustParseAddr(connection.LocalIP).Unmap(), uint16(connection.LocalPort))
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.runningLocked() || r.accessIndex == nil {
		return fmt.Errorf("transport closure observer unavailable")
	}
	owner, known, complete := r.accessIndex.Lookup(peer)
	if !known || !complete || owner.UUID != connection.UUID || owner.AcceptedAt.UnixMilli() != connection.AcceptedAt {
		return fmt.Errorf("transport closure ownership changed or unknown")
	}
	return CloseTransportPeer(TransportPeer{Local: local, Remote: peer})
}
