package connectionlog

import (
	"errors"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type wgReceived struct {
	data      []byte
	outgoing  bool
	truncated bool
}

type wgCapture interface {
	receive([]byte) (wgReceived, error)
	statistics() (uint64, uint64, error)
	close() error
}

// Caller holds op. The reader cannot attribute packets until the gate is ON.
func (w *WireGuardObserver) startCapture() error {
	c, err := openWGCapture(w.index)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.capture, w.captureDone, w.captureErr = c, make(chan struct{}), nil
	w.stats.LastError = ""
	done := w.captureDone
	w.mu.Unlock()
	go func() {
		defer close(done)
		buf := make([]byte, 65575)
		for {
			p, err := c.receive(buf)
			if err != nil {
				w.mu.Lock()
				// stopCapture detaches this exact socket before closing it.
				// RawConn.Read's platform close error need not be os.ErrClosed.
				if w.capture != c {
					w.mu.Unlock()
					return
				}
				w.captureErr = errors.Join(err, w.gap("capture_drop"))
				w.stats.LastError = w.captureErr.Error()
				w.mu.Unlock()
				return
			}
			w.mu.Lock()
			if w.enabled && !w.frozen {
				if err := w.consume(p); err != nil {
					w.captureErr = err
					w.stats.LastError = err.Error()
					w.mu.Unlock()
					return
				}
			}
			w.mu.Unlock()
		}
	}()
	return nil
}

// Caller holds op but not mu; closing the file wakes RawConn.Read and joins it.
func (w *WireGuardObserver) stopCapture() error {
	w.mu.Lock()
	c, done := w.capture, w.captureDone
	if c == nil {
		w.mu.Unlock()
		return nil
	}
	err := w.captureStats()
	w.capture = nil
	w.mu.Unlock()
	err = errors.Join(err, c.close())
	<-done
	return err
}

func (w *WireGuardObserver) captureStats() error {
	if w.capture == nil {
		return nil
	}
	packets, drops, err := w.capture.statistics()
	if err != nil {
		return errors.Join(err, w.gap("capture_drop"))
	}
	w.stats.KernelPackets += packets
	w.stats.KernelDrops += drops
	if drops != 0 {
		return w.gap("capture_drop")
	}
	return nil
}

func (w *WireGuardObserver) gap(reason string) error {
	defer w.notifyProducer()
	w.stats.Degraded = true
	if w.gaps[reason] {
		return nil
	}
	w.gaps[reason] = true
	now := nodeprov.HistoryTimestamp(w.now())
	from := now
	if !w.lastCheck.IsZero() {
		from = nodeprov.HistoryTimestamp(w.lastCheck)
	}
	return w.history.ReportGap(nodeprov.HistoryGap{
		ProducerID: "wireguard:" + w.options.Interface, ObserverBootID: w.history.BootID(),
		FromTime: &from, ToTime: &now, Reason: reason,
	})
}

func (w *WireGuardObserver) consume(raw wgReceived) error {
	defer w.notifyProducer()
	w.stats.Packets++
	if raw.truncated {
		w.stats.Truncated++
		return w.gap("capture_drop")
	}
	p, err := parseWGPacket(raw.data)
	if err != nil {
		if errors.Is(err, errWGUnsupportedPacket) {
			w.stats.Unsupported++
			return w.gap("unsupported_packet")
		}
		w.stats.DecodeErrors++
		return w.gap("decode_error")
	}
	addr := p.source
	if raw.outgoing {
		addr = p.destination
	}
	peer, ok := w.hosts[addr]
	if !ok {
		w.stats.UnknownPeer++
		return w.gap("identity_map_changed")
	}
	return w.observe(p, wgAttribution{peer: peer, outgoing: raw.outgoing})
}
