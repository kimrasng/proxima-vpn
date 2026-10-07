//go:build !relaye2e

package main

import "github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"

// egressDialer returns nil so production builds use the default net.Dialer.
// The relay E2E image substitutes a fixture dialer via the relaye2e build tag.
func egressDialer() deviceegress.DialFunc { return nil }
