package xray

import (
	"fmt"
	"net/netip"
	"time"
)

// TransportPeer is an observation of an established TCP socket, not a device
// identity or proof of authentication. Idle is Linux TCP_INFO's elapsed time
// since last_data_recv; it is not an application-level activity measurement.
type TransportPeer struct {
	Local  netip.AddrPort
	Remote netip.AddrPort
	Idle   time.Duration
}

func transportPortSet(ports []int) (map[uint16]bool, error) {
	set := make(map[uint16]bool, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("transport peers: invalid local port %d", port)
		}
		set[uint16(port)] = true
	}
	return set, nil
}
