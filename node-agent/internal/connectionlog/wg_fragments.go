package connectionlog

import (
	"net/netip"
	"time"
)

const (
	wgMaxFragments = 4096
	wgFragmentTTL  = 30 * time.Second
)

type wgFragmentKey struct {
	peer                string
	generation          uint64
	source, destination netip.Addr
	id                  uint32
	protocol            uint8
	outgoing            bool
}

type wgFragmentRange struct{ start, end uint32 }

type wgFragment struct {
	first      wgPacket
	expires    time.Time
	ranges     [32]wgFragmentRange
	count      int
	total, end uint32
	flow       *wgFlowWindow
}

func (w *WireGuardObserver) fragment(p wgPacket, a wgAttribution) (wgPacket, *wgFragment, error) {
	key := wgFragmentKey{peer: a.peer, generation: w.stats.MapGeneration, source: p.source, destination: p.destination, id: p.fragmentID, protocol: p.fragmentProtocol, outgoing: a.outgoing}
	f := w.fragments[key]
	if f != nil && !w.now().Before(f.expires) {
		delete(w.fragments, key)
		f = nil
	}
	if p.fragmentOffset == 0 {
		if f != nil {
			delete(w.fragments, key)
			return p, nil, errWGUnsupportedPacket
		}
		if len(w.fragments) >= wgMaxFragments {
			return p, nil, errWGUnsupportedPacket
		}
		f = &wgFragment{first: p, expires: w.now().Add(wgFragmentTTL)}
		w.fragments[key] = f
	} else if f == nil {
		return p, nil, errWGUnsupportedPacket
	}
	start, end := p.fragmentOffset, p.fragmentOffset+p.fragmentLength
	if f.count == len(f.ranges) {
		delete(w.fragments, key)
		return p, nil, errWGUnsupportedPacket
	}
	for _, r := range f.ranges[:f.count] {
		if start < r.end && end > r.start {
			delete(w.fragments, key)
			return p, nil, errWGUnsupportedPacket
		}
	}
	if (f.end != 0 && end > f.end) || (!p.more && f.end != 0 && end != f.end) {
		delete(w.fragments, key)
		return p, nil, errWGUnsupportedPacket
	}
	if !p.more {
		for _, r := range f.ranges[:f.count] {
			if r.end > end {
				delete(w.fragments, key)
				return p, nil, errWGUnsupportedPacket
			}
		}
		f.end = end
	}
	f.ranges[f.count] = wgFragmentRange{start, end}
	f.count++
	f.total += end - start
	p.protocol, p.sourcePort, p.destinationPort = f.first.protocol, f.first.sourcePort, f.first.destinationPort
	p.flags, p.sequence = 0, f.first.sequence
	// SYN establishes the generation on the first fragment. FIN/RST are
	// actionable only after every fragment range is observed without overlap.
	if start == 0 {
		p.flags = f.first.flags &^ uint8(5)
	}
	if f.end != 0 && f.total == f.end {
		p.flags |= f.first.flags & 5
		delete(w.fragments, key)
	}
	return p, f, nil
}
