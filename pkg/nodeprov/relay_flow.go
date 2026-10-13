package nodeprov

import (
	"fmt"
	"net/netip"
)

// RelayFlow links the tuple seen by an Exit to the source address seen by the
// Entry before masquerading. It is observation, not a physical-device identity.
// Transport is TCP: the initial supported path is VLESS Reality over TCP,
// including XUDP carried inside that tunnel.
type RelayFlow struct {
	PeerIP   string `json:"peer_ip"`
	PeerPort int    `json:"peer_port"`
	ExitIP   string `json:"exit_ip"`
	ExitPort int    `json:"exit_port"`
	ClientIP string `json:"client_ip"`
}

func (f RelayFlow) Validate() error {
	for _, raw := range []string{f.PeerIP, f.ExitIP, f.ClientIP} {
		ip, err := netip.ParseAddr(raw)
		if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("invalid relay flow IPv4 address %q", raw)
		}
	}
	if err := ValidatePort(f.PeerPort); err != nil {
		return fmt.Errorf("relay peer port: %w", err)
	}
	if err := ValidatePort(f.ExitPort); err != nil {
		return fmt.Errorf("relay exit port: %w", err)
	}
	return nil
}
