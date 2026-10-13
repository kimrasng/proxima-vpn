//go:build !linux

package xray

import "errors"

// TransportPeers is unsupported outside Linux; an empty query is always empty.
func TransportPeers(ports []int) ([]TransportPeer, error) {
	set, err := transportPortSet(ports)
	if err != nil {
		return nil, err
	}
	if len(set) == 0 {
		return []TransportPeer{}, nil
	}
	return nil, errors.New("transport peers: socket diagnostics unsupported on this platform")
}
