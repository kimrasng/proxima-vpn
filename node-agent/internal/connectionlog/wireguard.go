package connectionlog

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

var (
	ErrWireGuardUnsupported = errors.New("WireGuard observation unsupported")
	ErrWireGuardMap         = errors.New("WireGuard applied map unavailable or ambiguous")
	ErrWireGuardRecreated   = errors.New("WireGuard interface recreated; new observer required")
	ErrWireGuardClosed      = errors.New("WireGuard observer closed")
)

type WireGuardOptions struct {
	Interface    string
	RuntimeBuild string
}

// WireGuardPeer contains only public, successfully applied peer configuration.
type WireGuardPeer struct {
	PublicKey  string
	AllowedIPs []netip.Prefix
}

type WireGuardAppliedMap struct {
	InterfaceIndex int
	Peers          []WireGuardPeer
}

type WireGuardStats struct {
	KernelRelease string
	Packets       uint64
	KernelPackets uint64
	KernelDrops   uint64
	Truncated     uint64
	DecodeErrors  uint64
	Unsupported   uint64
	UnknownPeer   uint64
	StateOverflow uint64
	MapGeneration uint64
	Flows         int
	Fragments     int
	Capturing     bool
	Frozen        bool
	Degraded      bool
	LastError     string
}

// WireGuardObserver owns observation only, never WireGuard configuration.
// Lock order is op -> mu -> shared exporter gate. Capture only takes mu.
type WireGuardObserver struct {
	op          sync.Mutex
	mu          sync.Mutex
	options     WireGuardOptions
	history     *connectionhistory.Observer
	now         func() time.Time
	hosts       map[netip.Addr]string
	index       int
	enabled     bool
	frozen      bool
	closed      bool
	capture     wgCapture
	captureDone chan struct{}
	captureErr  error
	stats       WireGuardStats
	peers       map[string]*wgPeerWindow
	flows       map[wgFlowKey]*wgFlowWindow
	fragments   map[wgFragmentKey]*wgFragment
	gaps        map[string]bool
	lastCheck   time.Time
	wake        chan struct{}
	producerMu  sync.Mutex
	producer    *wgProducerSession
}

func NewWireGuardObserver(options WireGuardOptions) (*WireGuardObserver, error) {
	if options.Interface == "" || len(options.Interface) > 15 {
		return nil, fmt.Errorf("%w: interface name", ErrWireGuardUnsupported)
	}
	kernel, err := wgKernelRelease()
	if err != nil {
		return nil, err
	}
	capabilities := []string{}
	if wgCaptureSupported() {
		capabilities = []string{"activity_window", "hot_toggle", "inner_ip_bytes", "tcp_flow", "udp_destination"}
	}
	h, err := connectionhistory.NewObserver(connectionhistory.Options{
		ProducerID: "wireguard:" + options.Interface, Runtime: "wireguard",
		RuntimeBuild: options.RuntimeBuild,
		Capabilities: capabilities,
	})
	if err != nil {
		return nil, err
	}
	return &WireGuardObserver{
		options: options, history: h, now: time.Now, frozen: true,
		peers: make(map[string]*wgPeerWindow), flows: make(map[wgFlowKey]*wgFlowWindow),
		fragments: make(map[wgFragmentKey]*wgFragment), gaps: make(map[string]bool),
		wake:  make(chan struct{}, 1),
		stats: WireGuardStats{KernelRelease: kernel},
	}, nil
}

func (w *WireGuardObserver) Hello() nodeprov.HistoryHello { return w.history.Hello() }
func (w *WireGuardObserver) Resume(r nodeprov.HistoryResume) ([][]byte, error) {
	return w.history.Resume(r)
}
func (w *WireGuardObserver) Ack(a nodeprov.HistoryAck) error { return w.history.Ack(a) }
func (w *WireGuardObserver) Stats() WireGuardStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.stats
	s.Flows, s.Fragments, s.Capturing, s.Frozen = len(w.flows), len(w.fragments), w.capture != nil, w.frozen
	return s
}

// Apply is the only collection-control entrypoint. It serializes the packet
// boundary with the shared replay boundary, without any VPN configuration call.
func (w *WireGuardObserver) Apply(ctx context.Context, a nodeprov.HistoryApply) (nodeprov.HistoryApplied, error) {
	w.op.Lock()
	defer w.op.Unlock()
	if w.closed {
		return nodeprov.HistoryApplied{}, ErrWireGuardClosed
	}
	if err := ctx.Err(); err != nil {
		return nodeprov.HistoryApplied{}, err
	}
	if a.Enabled && !wgCaptureSupported() {
		return nodeprov.HistoryApplied{}, ErrWireGuardUnsupported
	}
	if a.Enabled && !w.enabled {
		if w.frozen {
			return nodeprov.HistoryApplied{}, ErrWireGuardMap
		}
		if err := w.verifyMap(ctx); err != nil {
			return nodeprov.HistoryApplied{}, err
		}
		if err := w.startCapture(); err != nil {
			return nodeprov.HistoryApplied{}, err
		}
	}
	w.mu.Lock()
	if w.enabled && !a.Enabled {
		// The reader may hold one packet outside the gate, and the kernel
		// receive queue may have an unobserved tail. Account for that boundary
		// uncertainty before the exporter emits its disabled barrier.
		if err := errors.Join(w.captureStats(), w.gap("capture_drop")); err != nil {
			w.mu.Unlock()
			return nodeprov.HistoryApplied{}, err
		}
	}
	wasEnabled := w.enabled
	applied, err := w.history.Apply(a)
	if err == nil {
		w.enabled = a.Enabled
		if a.Enabled && !wasEnabled {
			clear(w.gaps)
			w.stats.Degraded = false
			w.lastCheck = w.now()
		}
		w.notifyProducer()
	}
	w.mu.Unlock()
	if err != nil {
		if !w.enabled {
			err = errors.Join(err, w.stopCapture())
		}
		return applied, err
	}
	if !a.Enabled {
		err = w.stopCapture()
		w.mu.Lock()
		// Capture was blind while OFF. Forget windows without terminal records.
		err = errors.Join(err, w.clearWindows("idle_timeout"))
		w.mu.Unlock()
	}
	return applied, err
}

// Run provides periodic expiry, packet statistics and out-of-band map checks.
// The caller starts it once and joins it at shutdown.
func (w *WireGuardObserver) Run(ctx context.Context) error {
	ticker := time.NewTicker(connectionhistory.UpdateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), w.Close())
		case <-ticker.C:
			if err := w.maintain(ctx); err != nil {
				return err
			}
		}
	}
}

func (w *WireGuardObserver) maintain(ctx context.Context) error {
	w.op.Lock()
	defer w.op.Unlock()
	if w.closed {
		return ErrWireGuardClosed
	}
	if !w.enabled {
		return nil
	}
	if err := w.verifyMap(ctx); err != nil {
		return errors.Join(err, w.freeze())
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	clear(w.gaps)
	if err := w.captureErr; err != nil {
		return err
	}
	if err := w.captureStats(); err != nil {
		return err
	}
	if err := w.expire(w.now()); err != nil {
		return err
	}
	w.lastCheck = w.now()
	err := w.history.Snapshot()
	w.notifyProducer()
	return err
}

// Close shuts down observation; disappearance never fabricates a VPN end.
func (w *WireGuardObserver) Close() error {
	w.op.Lock()
	if w.closed {
		w.op.Unlock()
		return nil
	}
	w.mu.Lock()
	w.frozen = true
	err := w.gap("producer_lost")
	w.mu.Unlock()
	err = errors.Join(err, w.stopCapture())
	w.closed = true
	w.op.Unlock()
	w.closeProducer()
	return err
}
