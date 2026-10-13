//go:build !linux

package xray

import "testing"

func TestCloseTransportPeerUnsupported(t *testing.T) {
	if err := CloseTransportPeer(TransportPeer{}); err == nil {
		t.Fatal("unsupported platform returned success")
	}
}
