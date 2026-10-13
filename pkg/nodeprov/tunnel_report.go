package nodeprov

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
)

// TunnelSessionToken is a stable opaque key for one authenticated Exit
// transport generation. It includes both endpoints, the owning credential and
// the first authenticated acceptance time; it does not use an address alone.
func TunnelSessionToken(exitID string, connection TunnelConnection) (string, error) {
	exit, err := uuid.Parse(exitID)
	if err != nil {
		return "", fmt.Errorf("invalid session Exit identity")
	}
	if err := connection.Validate(); err != nil {
		return "", err
	}
	credential, err := uuid.Parse(connection.UUID)
	if err != nil {
		return "", fmt.Errorf("invalid tunnel credential UUID")
	}
	local := netip.AddrPortFrom(netip.MustParseAddr(connection.LocalIP).Unmap(), uint16(connection.LocalPort))
	peer := netip.AddrPortFrom(netip.MustParseAddr(connection.PeerIP).Unmap(), uint16(connection.PeerPort))
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d", exit.String(), credential.String(), local, peer, connection.AcceptedAt)))
	return hex.EncodeToString(sum[:]), nil
}

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
		if err != nil {
			return fmt.Errorf("invalid tunnel address %q", raw)
		}
		addr = addr.Unmap()
		if addr.IsUnspecified() || addr.IsMulticast() {
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
