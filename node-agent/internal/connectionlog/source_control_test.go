package connectionlog

import (
	"context"
	"sync"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// A real shared observer plus the same capture gate a WG wrapper must own.
type captureProducer struct {
	observer *connectionhistory.Observer
	mu       sync.Mutex
	changed  chan struct{}
	open     bool
}

func (p *captureProducer) Hello() nodeprov.HistoryHello { return p.observer.Hello() }
func (p *captureProducer) Resume(r nodeprov.HistoryResume) ([][]byte, error) {
	return p.observer.Resume(r)
}
func (p *captureProducer) Ack(a nodeprov.HistoryAck) error { return p.observer.Ack(a) }
func (p *captureProducer) Changes() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}
func (p *captureProducer) publish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	close(p.changed)
	p.changed = make(chan struct{})
}
func (p *captureProducer) Apply(ctx context.Context, a nodeprov.HistoryApply) (nodeprov.HistoryApplied, error) {
	if err := ctx.Err(); err != nil {
		return nodeprov.HistoryApplied{}, err
	}
	applied, err := p.observer.Apply(a)
	if err == nil {
		p.mu.Lock()
		p.open = a.Enabled
		p.mu.Unlock()
		p.publish()
	}
	return applied, err
}

func TestControlSource_UsesWrapperCaptureGateAndStreamsReplayWithoutDeadlock(t *testing.T) {
	// Given a real observer whose capture gate exists only in its wrapper.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, api := newWireAPI(t)
	observer, err := connectionhistory.NewObserver(connectionhistory.Options{ProducerID: "wireguard:wg0",
		Runtime: "wireguard", RuntimeBuild: "wrapped-test", Capabilities: []string{"hot_toggle"}})
	must(t, err)
	wrapped := &captureProducer{observer: observer, changed: make(chan struct{})}
	source, err := NewControlSource("wireguard:wg0", "wireguard", wrapped)
	must(t, err)
	m := newManager(t, api, t.TempDir())
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(source))
	must(t, m.Poll(ctx))
	stop := startSource(t, ctx, m, source.ID())
	defer stop()
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" })
	wrapped.mu.Lock()
	open := wrapped.open
	wrapped.mu.Unlock()
	if !open {
		t.Fatal("raw exporter apply bypassed wrapper capture gate")
	}
	// When a replay burst and a live disable traverse the direct transport.
	for i := 0; i < 12; i++ {
		tunnel, err := observer.NewTunnel(connectionhistory.TunnelInfo{Protocol: "wireguard",
			InboundTag: "wg0", LabelKind: "wg_public_key", Label: "authenticated-key", Scope: "activity_window",
			PartialStart: true})
		must(t, err)
		must(t, tunnel.Close(connectionhistory.End{Reason: "idle_timeout", Semantics: "activity_expired"}))
	}
	wrapped.publish()
	through := observer.Hello().LastSequence
	waitStatus(t, m, func(s Status) bool { return s.Producers[0].LastDurableSequence == through })
	wire.mu.Lock()
	wire.cfg.Enabled, wire.cfg.Revision = false, 2
	wire.mu.Unlock()
	must(t, m.Poll(ctx))
	waitStatus(t, m, func(s Status) bool { return s.State == "off" && s.AppliedRevision == 2 })
	must(t, m.UploadOnce(ctx))
	// Then OFF crosses the wrapper and HTTP drains without a raw-observer bypass.
	wrapped.mu.Lock()
	defer wrapped.mu.Unlock()
	if wrapped.open {
		t.Fatal("wrapper capture remained open after durable OFF")
	}
}
