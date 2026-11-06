package connectionlog

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

var _ interface {
	ID() string
	Dial(context.Context) (net.Conn, error)
} = (*WireGuardObserver)(nil)

func wgProducerDial(t *testing.T, w *WireGuardObserver) (net.Conn, *bufio.Reader, nodeprov.HistoryHello) {
	t.Helper()
	c, err := w.Dial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	frame, err := nodeprov.DecodeHistoryControl(line)
	if err != nil {
		t.Fatal(err)
	}
	hello, ok := frame.(nodeprov.HistoryHello)
	if !ok || hello.ProducerID != w.ID() {
		t.Fatalf("hello %v", frame)
	}
	return c, r, hello
}

func wgSend(t *testing.T, c net.Conn, frame nodeprov.HistoryControl) {
	t.Helper()
	if err := json.NewEncoder(c).Encode(frame); err != nil {
		t.Fatal(err)
	}
}

func TestWGProducerApplyCannotBypassCapture(t *testing.T) {
	// Given a producer with no successfully applied WireGuard map.
	w, err := NewWireGuardObserver(WireGuardOptions{Interface: "wgtest", RuntimeBuild: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	c, r, hello := wgProducerDial(t, w)
	wgSend(t, c, nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID})
	epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	// When the agent asks to enable over the real producer protocol.
	wgSend(t, c, nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: []nodeprov.HistoryBinding{}})
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	frame, err := nodeprov.DecodeHistoryControl(line)
	if err != nil {
		t.Fatal(err)
	}
	// Then wrapper preconditions reject it, rather than enabling the shared gate.
	if _, ok := frame.(nodeprov.HistoryError); !ok {
		t.Fatalf("bypassed capture: %T", frame)
	}
	if w.Hello().Enabled || w.Stats().Capturing {
		t.Fatal("enabled without capture")
	}
}

func TestWGProducerACKBarrierAndReconnect(t *testing.T) {
	// Given live packet events and an agent-like immediate ACK reader.
	w, _ := wgTestObserver(t)
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 64; i++ {
		b := wgTestPacket(17, 0)
		b[21] = byte(i + 1)
		wgFeed(t, w, b, false)
	}
	c, r, hello := wgProducerDial(t, w)
	wgSend(t, c, nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID})
	off := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 2, CollectionEpoch: hello.CollectionEpoch, Bindings: []nodeprov.HistoryBinding{}}
	// When OFF races replay, each event is ACKed before reading the next one.
	wgSend(t, c, off)
	var delivered nodeprov.HistoryU64
	var applied nodeprov.HistoryApplied
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		if e, err := nodeprov.DecodeHistoryEvent(line); err == nil {
			delivered = e.Sequence
			wgSend(t, c, nodeprov.HistoryAck{Op: "ack", ObserverBootID: hello.ObserverBootID, ThroughSequence: delivered})
			continue
		}
		frame, err := nodeprov.DecodeHistoryControl(line)
		if err != nil {
			t.Fatal(err)
		}
		var ok bool
		applied, ok = frame.(nodeprov.HistoryApplied)
		if !ok {
			t.Fatalf("control %v", frame)
		}
		break
	}
	if applied.BarrierSequence != delivered || w.Hello().Enabled {
		t.Fatal("applied overtook durable events")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c2, r2, h2 := wgProducerDial(t, w)
	wgSend(t, c2, nodeprov.HistoryResume{Op: "resume", ObserverBootID: h2.ObserverBootID, AfterSequence: delivered})
	wgSend(t, c2, off)
	line, err := r2.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	frame, err := nodeprov.DecodeHistoryControl(line)
	if err != nil {
		t.Fatal(err)
	}
	again, ok := frame.(nodeprov.HistoryApplied)
	// Then reconnect and idempotent control retain boot and barrier.
	if !ok || again.BarrierSequence != delivered || h2.ObserverBootID != hello.ObserverBootID {
		t.Fatalf("reconnect %v", frame)
	}
}

func TestWGProducerRejectsUndeliveredACK(t *testing.T) {
	// Given an empty resumed producer.
	w, err := NewWireGuardObserver(WireGuardOptions{Interface: "wgtest", RuntimeBuild: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	c, r, h := wgProducerDial(t, w)
	wgSend(t, c, nodeprov.HistoryResume{Op: "resume", ObserverBootID: h.ObserverBootID})
	// When ACK claims a sequence that was never delivered.
	wgSend(t, c, nodeprov.HistoryAck{Op: "ack", ObserverBootID: h.ObserverBootID, ThroughSequence: 100})
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	frame, err := nodeprov.DecodeHistoryControl(line)
	if err != nil {
		t.Fatal(err)
	}
	// Then it is refused by this connection's delivery bound.
	e, ok := frame.(nodeprov.HistoryError)
	if !ok || e.Code != "export_unavailable" {
		t.Fatalf("%v", frame)
	}
}
