package connectionlog

import (
	"net/netip"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

const (
	wgPeerIdle = 60 * time.Second
	wgTCPIdle  = 300 * time.Second
	wgUDPIdle  = 60 * time.Second
	wgMaxFlows = 16384
)

type wgAttribution struct {
	peer     string
	outgoing bool
}
type wgFlowKey struct {
	peer          string
	local, remote netip.AddrPort
	protocol      uint8
}
type wgPeerWindow struct {
	handle *connectionhistory.Tunnel
	last   time.Time
}
type wgFlowWindow struct {
	handle   *connectionhistory.Flow
	last     time.Time
	syn      bool
	sequence uint32
	fin      uint8
}

func (w *WireGuardObserver) observe(p wgPacket, a wgAttribution) error {
	now := w.now()
	if err := w.expire(now); err != nil {
		return err
	}
	var fragment *wgFragment
	if p.fragmented {
		var err error
		p, fragment, err = w.fragment(p, a)
		if err != nil {
			w.stats.Unsupported++
			return w.gap("unsupported_packet")
		}
	}
	key := wgFlowKey{peer: a.peer, local: netip.AddrPortFrom(p.source, p.sourcePort), remote: netip.AddrPortFrom(p.destination, p.destinationPort), protocol: p.protocol}
	if a.outgoing {
		key.local, key.remote = key.remote, key.local
	}
	f := w.flows[key]
	syn := p.protocol == 6 && p.flags&0x12 == 2
	if fragment != nil && fragment.flow != nil && fragment.flow != f {
		w.stats.Unsupported++
		return w.gap("unsupported_packet")
	}
	if f != nil && syn && (!f.syn || f.sequence != p.sequence) {
		if err := w.closeFlow(key, "tuple_reused"); err != nil {
			return err
		}
		f = nil
	}
	peer := w.peers[a.peer]
	if f == nil {
		if len(w.flows) >= wgMaxFlows {
			w.stats.StateOverflow++
			return w.gap("capture_drop")
		}
		if peer == nil {
			t, err := w.history.NewTunnel(connectionhistory.TunnelInfo{
				Protocol: "wireguard", InboundTag: w.options.Interface, LabelKind: "wg_public_key",
				Label: a.peer, Scope: "activity_window", PartialStart: true,
			})
			if err != nil {
				return err
			}
			peer = &wgPeerWindow{handle: t, last: now}
			w.peers[a.peer] = peer
		}
		ip, port := key.remote.Addr().String(), int(key.remote.Port())
		transport := "udp"
		if p.protocol == 6 {
			transport = "tcp"
		}
		h, err := w.history.NewFlow(connectionhistory.FlowInfo{
			Tunnel: peer.handle, Destination: nodeprov.HistoryDestination{Transport: transport, ObservedIP: &ip, ObservedPort: &port},
			DestinationQuality: "observed", Scope: "activity_window", Layer: "inner_ip_observed", PartialStart: true,
		})
		if err != nil {
			return err
		}
		f = &wgFlowWindow{handle: h, last: now, syn: syn, sequence: p.sequence}
		w.flows[key] = f
		// A loss interval may precede this owner. Mark it unknown as well.
		if len(w.gaps) > 0 {
			for reason := range w.gaps {
				delete(w.gaps, reason)
				if err := w.gap(reason); err != nil {
					return err
				}
				break
			}
		}
	}
	if fragment != nil {
		fragment.flow = f
	}
	f.last, peer.last = now, now
	up, down := p.length, uint64(0)
	if a.outgoing {
		up, down = 0, p.length
	}
	if err := f.handle.Observe(up, down); err != nil {
		return err
	}
	if p.protocol != 6 {
		return nil
	}
	if p.flags&4 != 0 {
		return w.closeFlow(key, "rst")
	}
	if p.flags&1 != 0 {
		bit := uint8(1)
		if a.outgoing {
			bit = 2
		}
		f.fin |= bit
		if f.fin == 3 {
			return w.closeFlow(key, "fin")
		}
	}
	return nil
}

func (w *WireGuardObserver) closeFlow(key wgFlowKey, reason string) error {
	f := w.flows[key]
	// PACKET_STATISTICS is reset on read. A short flow must not publish a
	// false final before the periodic drop check notices an earlier loss.
	if err := w.captureStats(); err != nil {
		return err
	}
	end := connectionhistory.End{Reason: reason, Semantics: "observation_segment_closed"}
	if reason == "idle_timeout" {
		end.Semantics = "activity_expired"
	}
	if err := f.handle.Close(end); err != nil {
		return err
	}
	delete(w.flows, key)
	for k, v := range w.fragments {
		if v.flow == f {
			delete(w.fragments, k)
		}
	}
	return nil
}

func (w *WireGuardObserver) expire(now time.Time) error {
	defer w.notifyProducer()
	for key, f := range w.fragments {
		if !now.Before(f.expires) {
			delete(w.fragments, key)
			if err := w.gap("unsupported_packet"); err != nil {
				return err
			}
		}
	}
	for key, f := range w.flows {
		ttl := wgUDPIdle
		if key.protocol == 6 {
			ttl = wgTCPIdle
		}
		if now.Sub(f.last) >= ttl || now.Sub(w.peers[key.peer].last) >= wgPeerIdle {
			if err := w.closeFlow(key, "idle_timeout"); err != nil {
				return err
			}
		}
	}
	for key, p := range w.peers {
		if now.Sub(p.last) >= wgPeerIdle {
			if err := p.handle.Close(connectionhistory.End{Reason: "idle_timeout", Semantics: "activity_expired"}); err != nil {
				return err
			}
			delete(w.peers, key)
		}
	}
	return nil
}

func (w *WireGuardObserver) clearWindows(reason string) error {
	for key := range w.flows {
		if err := w.closeFlow(key, reason); err != nil {
			return err
		}
	}
	for key, p := range w.peers {
		if err := p.handle.Close(connectionhistory.End{Reason: reason, Semantics: "observation_segment_closed"}); err != nil {
			return err
		}
		delete(w.peers, key)
	}
	clear(w.fragments)
	return nil
}
