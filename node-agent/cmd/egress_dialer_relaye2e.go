//go:build relaye2e

package main

import (
	"context"
	"net"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
)

// relayE2EFixtureHost is a documentation-only (TEST-NET-2) address. It passes
// production destination validation unchanged; only this test-tagged binary
// maps it, for the marker ports alone, to the Exit's loopback fixtures.
const relayE2EFixtureHost = "198.51.100.10"

var relayE2EFixturePorts = map[string]bool{"9090": true, "9091": true}

func egressDialer() deviceegress.DialFunc {
	base := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err == nil && host == relayE2EFixtureHost && relayE2EFixturePorts[port] {
			address = net.JoinHostPort("127.0.0.1", port)
		}
		return base.DialContext(ctx, network, address)
	}
}
