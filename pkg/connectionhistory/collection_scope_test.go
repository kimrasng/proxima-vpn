package connectionhistory

import (
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func scopedInfo(tunnel *Tunnel, host string) FlowInfo {
	return FlowInfo{Tunnel: tunnel, Destination: nodeprov.HistoryDestination{
		RequestedHost: ptr(host), RequestedPort: ptr(443), Transport: "udp",
	}}
}

func TestCollectionScopeOFFDoesNotRetainNewDestinations(t *testing.T) {
	o := testObserver(t)
	flows := make(map[string]*Flow)
	resets, calls := 0, 0
	scope := o.NewCollectionScope(func() { resets++; clear(flows) })
	for range 100 {
		check(t, scope.WithEnabled(func(g *CollectionGate) error {
			calls++
			info := scopedInfo(nil, "off-only.test")
			info.Protocol = "vless"
			f, err := g.NewFlow(info)
			if err != nil {
				return err
			}
			flows["off-only.test"] = f
			return g.Observe(f, 19, 7)
		}))
	}
	if calls != 0 || len(flows) != 0 || len(scope.flows) != 0 || len(o.flows) != 0 || o.flowCounter != 0 || o.sequence != 0 {
		t.Fatal("OFF callback retained history destinations")
	}
	check(t, scope.Close(normalEnd))
	if resets != 1 || len(o.scopes) != 0 || scope.reset != nil {
		t.Fatal("OFF scope closure did not unregister/reset")
	}
	if err := scope.WithEnabled(func(*CollectionGate) error { t.Fatal("closed callback"); return nil }); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestCollectionScopeDisableRetiresWithoutEndsAndReenableIsPartial(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	tunnel, ordinary := owners(t, o)
	flows := make(map[string]*Flow)
	resets := 0
	var resetSequence nodeprov.HistoryU64
	scope := o.NewCollectionScope(func() {
		clear(flows)
		resets++
		resetSequence = o.sequence
	})
	var first *Flow
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		var err error
		first, err = g.NewFlow(scopedInfo(tunnel, "first.test"))
		if err != nil {
			return err
		}
		flows["first.test"] = first
		if err := g.SetDestination(first, ptr("effective.test"), &nodeprov.HistoryPeer{IP: "192.0.2.1", Port: 8443}); err != nil {
			return err
		}
		return g.Observe(first, 19, 7)
	}))
	off := applyTest(t, o, 2, false, epochOne)
	if resets != 1 || resetSequence != off.BarrierSequence || len(flows) != 0 || len(scope.flows) != 0 ||
		len(o.flows) != 1 || ordinary.closed || tunnel.children != 1 || first.info.Destination.RequestedHost != nil {
		t.Fatal("disable did not atomically clear only scoped destinations")
	}
	if err := first.Observe(1, 1); !errors.Is(err, ErrClosed) {
		t.Fatal("retired scoped flow accepted a late observation")
	}
	var updates int
	for _, e := range events(t, o, 0) {
		if e.FlowID == nil || *e.FlowID != first.ID() {
			continue
		}
		if e.Kind == "flow_end" {
			t.Fatal("disable fabricated a scoped flow end")
		}
		if e.Kind == "flow_update" {
			updates++
			if *e.Bytes.Upload != 19 || *e.Bytes.Download != 7 || e.Bytes.Final || e.Lifecycle.EndedAt != nil ||
				*e.Destination.EffectiveHost != "effective.test" || *e.Destination.ObservedIP != "192.0.2.1" {
				t.Fatal("disable lost the last nonterminal scoped sample")
			}
		}
	}
	if updates != 1 {
		t.Fatal("missing scoped disable snapshot")
	}
	check(t, scope.WithEnabled(func(*CollectionGate) error { t.Fatal("OFF callback executed"); return nil }))
	applyTest(t, o, 3, true, epochTwo)
	if len(scope.flows) != 0 || len(flows) != 0 {
		t.Fatal("enable rebuilt a destination journal")
	}
	var next *Flow
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		var err error
		next, err = g.NewFlow(scopedInfo(tunnel, "first.test"))
		if err != nil {
			return err
		}
		flows["first.test"] = next
		return g.Observe(next, 2, 3)
	}))
	check(t, scope.Close(normalEnd))
	if next.ID() == first.ID() || len(flows) != 0 || len(o.scopes) != 0 || resets != 2 {
		t.Fatal("scope close/re-enable lifecycle")
	}
	var starts, ends int
	for _, e := range events(t, o, off.BarrierSequence) {
		if e.FlowID == nil || *e.FlowID != next.ID() {
			continue
		}
		switch e.Kind {
		case "flow_start":
			starts++
			if !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil || *e.Bytes.Upload != 0 || *e.Bytes.Download != 0 {
				t.Fatal("re-enabled scope lacks partial zero baseline")
			}
		case "flow_end":
			ends++
			if *e.Bytes.Upload != 2 || *e.Bytes.Download != 3 || !e.Bytes.Final {
				t.Fatal("new scoped counters include old epoch")
			}
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatal("scoped generation start/end count")
	}
}

func TestCollectionGateCloseAndOwnership(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	tunnel, _ := owners(t, o)
	one, two := o.NewCollectionScope(nil), o.NewCollectionScope(nil)
	var f *Flow
	check(t, one.WithEnabled(func(g *CollectionGate) error {
		var err error
		f, err = g.NewFlow(scopedInfo(tunnel, "owned.test"))
		return err
	}))
	check(t, two.WithEnabled(func(g *CollectionGate) error {
		for _, err := range []error{g.Observe(f, 1, 0), g.SetDestination(f, nil, nil), g.CloseFlow(f, normalEnd)} {
			if !errors.Is(err, ErrScopeFlow) {
				t.Fatalf("foreign flow accepted: %v", err)
			}
		}
		return nil
	}))
	check(t, one.WithEnabled(func(g *CollectionGate) error {
		if err := g.Observe(f, 19, 7); err != nil {
			return err
		}
		return g.CloseFlow(f, normalEnd)
	}))
	if len(one.flows) != 0 {
		t.Fatal("CloseFlow retained a scope registry entry")
	}
	check(t, one.Close(normalEnd))
	check(t, two.Close(normalEnd))
}
