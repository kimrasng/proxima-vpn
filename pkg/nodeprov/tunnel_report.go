package nodeprov

import (
	"fmt"
	"net/netip"

	"github.com/google/uuid"
)

// TunnelConnection is a node-observed authenticated TCP transport peer, not a
// destination stream and not a client-provided installation identifier.
type TunnelConnection struct {
	UUID       string `json:"uuid"`
	PeerIP     string `json:"peer_ip"`
	PeerPort   int    `json:"peer_port"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	AcceptedAt int64  `json:"accepted_at"`
	IdleMillis int64  `json:"idle_millis"`
}

func (c TunnelConnection) Validate() error {
	if _, err := uuid.Parse(c.UUID); err != nil {
		return fmt.Errorf("invalid tunnel credential UUID")
	}
	for _, raw := range []string{c.PeerIP, c.LocalIP} {
		addr, err := netip.ParseAddr(raw)
		if err != nil || addr.IsUnspecified() || addr.IsMulticast() {
			return fmt.Errorf("invalid tunnel address %q", raw)
		}
	}
	if err := ValidatePort(c.PeerPort); err != nil {
		return err
	}
	if err := ValidatePort(c.LocalPort); err != nil {
		return err
	}
	if c.AcceptedAt <= 0 || c.IdleMillis < 0 {
		return fmt.Errorf("invalid tunnel timestamps")
	}
	return nil
}

// TunnelReport must be known and fresh before the server can use it to enforce
// limits. Unknown peers or partial socket/log observations make Known false.
type TunnelReport struct {
	Known       bool               `json:"known"`
	Connections []TunnelConnection `json:"connections"`
}
