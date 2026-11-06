package connectionlog

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManager_RunPollsIndependentConfigAndDrainsWhileOff(t *testing.T) {
	// Given the production worker loop with an explicit tick subscription.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, api := newWireAPI(t)
	obs, path := observer(t, ctx, "xray")
	ticks := make(chan time.Time)
	m, err := NewManager(Options{API: api, Directory: t.TempDir(), Ticks: ticks,
		Clock: func() time.Time { return testNow }})
	must(t, err)
	must(t, m.Register(NewUnixSource("xray", path)))
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("manager workers did not join")
		}
		must(t, m.Close())
	}()
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" })
	boot := obs.BootID()
	wire.mu.Lock()
	wire.cfg.Enabled, wire.cfg.Revision = false, 2
	wire.mu.Unlock()
	// When the next independent policy tick disables the live exporter.
	select {
	case ticks <- testNow:
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not accept policy tick")
	}
	waitStatus(t, m, func(s Status) bool { return s.State == "off" && s.AppliedRevision == 2 })
	// Then no process replacement occurs, and old records drain while OFF.
	if obs.BootID() != boot || obs.Hello().Enabled {
		t.Fatal("hot disable changed runtime lifetime")
	}
	select {
	case ticks <- testNow:
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not accept drain tick")
	}
	waitStatus(t, m, func(s Status) bool { return s.State == "off" && s.QueueBytes == 0 })
	wire.mu.Lock()
	defer wire.mu.Unlock()
	if len(wire.bodies) == 0 {
		t.Fatal("Run never uploaded durable events through HTTP")
	}
}

func TestManager_PolicyTickDisablesExporterWhileHTTPUploadIsStalled(t *testing.T) {
	// Given a real HTTP POST held open independently of the config GET endpoint.
	ctx, cancel := context.WithCancel(context.Background())
	wire, api := newWireAPI(t)
	obs, path := observer(t, ctx, "xray")
	gate, entered := make(chan struct{}), make(chan struct{}, 1)
	wire.mu.Lock()
	wire.postGate, wire.postEntered = gate, entered
	wire.mu.Unlock()
	ticks := make(chan time.Time)
	m, err := NewManager(Options{API: api, Directory: t.TempDir(), Ticks: ticks})
	must(t, err)
	must(t, m.Register(NewUnixSource("xray", path)))
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	defer func() {
		cancel()
		close(gate)
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("stalled upload worker did not join")
		}
		must(t, m.Close())
	}()
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" })
	select {
	case ticks <- testNow:
	case <-time.After(5 * time.Second):
		t.Fatal("manager's upload blocked its policy tick")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP upload did not reach the real endpoint")
	}
	wire.mu.Lock()
	wire.cfg.Enabled, wire.cfg.Revision = false, 2
	wire.mu.Unlock()
	boot := obs.BootID()
	// When a subsequent independent tick publishes desired OFF.
	select {
	case ticks <- testNow:
	case <-time.After(5 * time.Second):
		t.Fatal("stalled POST prevented an independent policy fetch")
	}
	s := waitStatus(t, m, func(s Status) bool { return s.AppliedRevision == 2 && !s.Enabled })
	// Then all source barriers apply while the earlier HTTP POST is still blocked.
	if obs.Hello().Enabled || obs.BootID() != boot || s.CollectionEpoch == nil {
		t.Fatal("independent disable did not cross the existing runtime's barrier")
	}
}
