package xray

import (
	"fmt"
	"sort"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// TunnelReport joins authenticated access-log ownership with live TCP socket
// observations. Handshakes not yet authenticated, missed log records and
// conflicting ownership make the snapshot unknown, never a smaller count.
func (r *XrayRunner) TunnelReport() (nodeprov.TunnelReport, error) {
	r.mu.Lock()
	if !r.runningLocked() || r.accessIndex == nil {
		r.mu.Unlock()
		return nodeprov.TunnelReport{}, fmt.Errorf("xray transport observer unavailable")
	}
	index, generation := r.accessIndex, r.exited
	r.mu.Unlock()
	ports, err := tunnelPorts(r.RuntimeConfig())
	if err != nil {
		return nodeprov.TunnelReport{}, err
	}
	peers, err := TransportPeers(ports)
	if err != nil {
		return nodeprov.TunnelReport{}, err
	}
	report := joinTransportOwners(index, peers)
	r.mu.Lock()
	defer r.mu.Unlock()
	if generation != r.exited || !r.runningLocked() {
		return nodeprov.TunnelReport{}, fmt.Errorf("xray restarted during transport observation")
	}
	return report, nil
}

func joinTransportOwners(index *AccessIndex, peers []TransportPeer) nodeprov.TunnelReport {
	report := nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{}}
	for _, peer := range peers {
		owner, known, complete := index.Lookup(peer.Remote)
		if !known || !complete {
			report.Known = false
			continue
		}
		report.Connections = append(report.Connections, nodeprov.TunnelConnection{
			UUID: owner.UUID, PeerIP: peer.Remote.Addr().String(), PeerPort: int(peer.Remote.Port()),
			LocalIP: peer.Local.Addr().String(), LocalPort: int(peer.Local.Port()),
			AcceptedAt: owner.AcceptedAt.UnixMilli(), IdleMillis: int64(peer.Idle / time.Millisecond),
		})
	}
	sort.Slice(report.Connections, func(i, j int) bool {
		a, b := report.Connections[i], report.Connections[j]
		if a.PeerIP != b.PeerIP {
			return a.PeerIP < b.PeerIP
		}
		if a.PeerPort != b.PeerPort {
			return a.PeerPort < b.PeerPort
		}
		if a.LocalIP != b.LocalIP {
			return a.LocalIP < b.LocalIP
		}
		return a.LocalPort < b.LocalPort
	})
	return report
}
