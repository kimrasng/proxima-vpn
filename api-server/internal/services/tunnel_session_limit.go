package services

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// ObservedTunnelSession is a transport connection authenticated by an Exit.
// An Entry forwards it but is not another session owner. Destination streams
// inside that transport never produce additional slots.
type ObservedTunnelSession struct {
	ExitID     string                    `json:"exit_id"`
	Connection nodeprov.TunnelConnection `json:"connection"`
}

type TunnelSessionObservation struct {
	Complete bool                    `json:"complete"`
	Sessions []ObservedTunnelSession `json:"sessions"`
}

// CountTunnelSessions deduplicates repeated reports of the exact transport,
// not UUIDs or source addresses. Conflicting ownership of one tuple is unknown.
func CountTunnelSessions(observation TunnelSessionObservation) (int, error) {
	if !observation.Complete {
		return 0, fmt.Errorf("incomplete tunnel session observation")
	}
	type key struct {
		exit, local, peer   string
		localPort, peerPort int
	}
	seen := map[key]nodeprov.TunnelConnection{}
	for _, session := range observation.Sessions {
		if session.ExitID == "" {
			return 0, fmt.Errorf("missing Exit session identity")
		}
		c := session.Connection
		if err := c.Validate(); err != nil {
			return 0, err
		}
		k := key{session.ExitID, netip.MustParseAddr(c.LocalIP).Unmap().String(), netip.MustParseAddr(c.PeerIP).Unmap().String(), c.LocalPort, c.PeerPort}
		if previous, exists := seen[k]; exists {
			if previous.UUID != c.UUID || previous.AcceptedAt != c.AcceptedAt {
				return 0, fmt.Errorf("ambiguous transport session ownership")
			}
			continue
		}
		seen[k] = c
	}
	return len(seen), nil
}

// NewestTunnelSessions proposes excess sessions only from unambiguous start
// ordering. It does not treat an address as a device or close any connection.
func NewestTunnelSessions(observation TunnelSessionObservation, limit int) ([]ObservedTunnelSession, error) {
	count, err := CountTunnelSessions(observation)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || count <= limit {
		return []ObservedTunnelSession{}, nil
	}
	unique := map[string]ObservedTunnelSession{}
	for _, s := range observation.Sessions {
		c := s.Connection
		key := fmt.Sprintf("%s|%s:%d|%s:%d", s.ExitID, netip.MustParseAddr(c.LocalIP).Unmap(), c.LocalPort, netip.MustParseAddr(c.PeerIP).Unmap(), c.PeerPort)
		unique[key] = s
	}
	sessions := make([]ObservedTunnelSession, 0, len(unique))
	for _, s := range unique {
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Connection.AcceptedAt < sessions[j].Connection.AcceptedAt })
	if sessions[limit-1].Connection.AcceptedAt == sessions[limit].Connection.AcceptedAt {
		return nil, fmt.Errorf("ambiguous session cutoff")
	}
	return sessions[limit:], nil
}
