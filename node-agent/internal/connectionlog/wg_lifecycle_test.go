package connectionlog

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestWGTCPIdleWhilePeerRemainsActive(t *testing.T) {
	// Given an idle TCP tuple whose peer continues sending other traffic.
	w, now := wgTestObserver(t)
	wgFeed(t, w, wgTestPacket(6, 2), false)
	// When UDP keeps the peer window alive through TCP's 300-second deadline.
	for i := 0; i < 6; i++ {
		*now = now.Add(50 * time.Second)
		b := wgTestPacket(17, 0)
		binary.BigEndian.PutUint16(b[20:22], uint16(41000+i))
		wgFeed(t, w, b, false)
	}
	// Then TCP closes at its own deadline, without changing the activity parent.
	tunnels, ends := 0, 0
	for _, e := range wgEvents(t, w) {
		if e.Kind == "tunnel_start" {
			tunnels++
		}
		if e.Kind == "flow_end" && e.Destination.Transport == "tcp" {
			ends++
			if *e.Lifecycle.EndReason != "idle_timeout" {
				t.Fatal("wrong TCP expiry reason")
			}
		}
	}
	if tunnels != 1 || ends != 1 {
		t.Fatalf("parents %d TCP ends %d", tunnels, ends)
	}
}

func TestWGDisableRaceEmitsNoTerminalOrPostBarrierDetail(t *testing.T) {
	// Given many packet callbacks sharing the same observation gate.
	w, _ := wgTestObserver(t)
	b := wgTestPacket(17, 0)
	wgFeed(t, w, b, false)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for n := 0; n < 100; n++ {
				w.mu.Lock()
				if w.enabled && !w.frozen {
					if err := w.consume(wgReceived{data: b}); err != nil {
						t.Error(err)
					}
				}
				w.mu.Unlock()
			}
		}()
	}
	// When disable races those callbacks.
	close(start)
	hello := w.Hello()
	off, err := w.Apply(t.Context(), nodeprov.HistoryApply{Op: "apply", ConfigRevision: 2, CollectionEpoch: hello.CollectionEpoch, Bindings: []nodeprov.HistoryBinding{}})
	if err != nil {
		t.Fatal(err)
	}
	group.Wait()
	// Then the durable barrier is last, and no application end was invented.
	for _, e := range wgEvents(t, w) {
		if e.Kind == "flow_end" || e.Kind == "tunnel_end" || e.Sequence > off.BarrierSequence {
			t.Fatalf("invalid disable event %+v", e)
		}
	}
	if len(w.flows) != 0 || len(w.fragments) != 0 || w.Hello().LastSequence != off.BarrierSequence {
		t.Fatal("OFF retained detailed state")
	}
}

func TestWGRunCancellationClosesObservation(t *testing.T) {
	// Given an OFF observer and an already-canceled context.
	w, err := NewWireGuardObserver(WireGuardOptions{Interface: "wgtest", RuntimeBuild: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// When the worker consumes cancellation.
	err = w.Run(ctx)
	// Then it returns that cancellation and rejects further controls.
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = w.Apply(t.Context(), nodeprov.HistoryApply{})
	if !errors.Is(err, ErrWireGuardClosed) {
		t.Fatal(err)
	}
}
