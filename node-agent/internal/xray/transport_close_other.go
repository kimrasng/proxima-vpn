//go:build !linux

package xray

import "errors"

// CloseTransportPeer is unsupported outside Linux.
func CloseTransportPeer(peer TransportPeer) error {
	return errors.New("close transport peer: SOCK_DESTROY unsupported on this platform")
}
