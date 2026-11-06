package connectionhistory

import (
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestCollectionGateReparentPreservesScopeAndDisableRetirement(t *testing.T) {
	// Given a live history-only destination and a replacement authenticated owner.
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	oldParent, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "old"})
	check(t, err)
	newParent, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "new"})
	check(t, err)
	flows := make(map[string]*Flow)
	scope := o.NewCollectionScope(func() { clear(flows) })
	other := o.NewCollectionScope(nil)
	var first, next *Flow
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		var err error
		first, err = g.NewFlow(scopedInfo(oldParent, "migrating.test"))
		if err != nil {
			return err
		}
		flows["migrating.test"] = first
		if err := g.SetDestination(first, ptr("effective.test"), &nodeprov.HistoryPeer{IP: "192.0.2.3", Port: 443}); err != nil {
			return err
		}
		return g.Observe(first, 19, 7)
	}))
	check(t, other.WithEnabled(func(g *CollectionGate) error {
		if _, err := g.Reparent(first, newParent); !errors.Is(err, ErrScopeFlow) {
			t.Fatal("foreign scope reparent accepted", err)
		}
		return nil
	}))
	before := o.sequence

	// When reparent and the replacement map publication share one enabled callback.
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		var err error
		next, err = g.Reparent(first, newParent)
		if err != nil {
			return err
		}
		flows["migrating.test"] = next
		if _, err := g.Reparent(first, oldParent); !errors.Is(err, ErrClosed) {
			t.Fatal("closed segment reparent accepted", err)
		}
		return g.Observe(next, 2, 3)
	}))

	// Then the old end and fresh zero start are adjacent and tracking moves once.
	segment := events(t, o, before)
	if len(segment) != 2 || segment[0].Kind != "flow_end" || segment[1].Kind != "flow_start" ||
		*segment[0].FlowID != first.ID() || *segment[1].FlowID != next.ID() || first.ID() == next.ID() {
		t.Fatal("gate reparent did not atomically replace the segment")
	}
	if *segment[0].Bytes.Upload != 19 || *segment[0].Bytes.Download != 7 ||
		*segment[0].Lifecycle.EndReason != "parent_changed" ||
		*segment[1].Bytes.Upload != 0 || *segment[1].Bytes.Download != 0 ||
		*segment[1].TunnelID != newParent.ID() ||
		*segment[1].Destination.RequestedHost != "migrating.test" ||
		*segment[1].Destination.EffectiveHost != "effective.test" ||
		*segment[1].Destination.ObservedIP != "192.0.2.3" {
		t.Fatal("gate reparent lost counters, destination or parent")
	}
	if len(scope.flows) != 1 || scope.flows[next.counter] != next || len(o.flows) != 1 ||
		oldParent.children != 0 || newParent.children != 1 || flows["migrating.test"] != next {
		t.Fatal("gate reparent lost scoped registration")
	}
	applyTest(t, o, 2, false, epochOne)
	if len(flows) != 0 || len(scope.flows) != 0 || len(o.flows) != 0 ||
		newParent.children != 0 || !next.closed || next.info.Destination.RequestedHost != nil {
		t.Fatal("disable retained the migrated history destination")
	}
	for _, e := range events(t, o, before) {
		if e.Kind == "flow_end" && *e.FlowID == next.ID() {
			t.Fatal("disable fabricated a migrated flow end")
		}
	}
	check(t, scope.Close(normalEnd))
	check(t, other.Close(normalEnd))
	check(t, oldParent.Close(normalEnd))
	check(t, newParent.Close(normalEnd))
}

func TestCollectionGateInvalidReparentLeavesScopedFlowLive(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	tunnel, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "live"})
	check(t, err)
	foreign := testObserver(t)
	foreignParent, err := foreign.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "foreign"})
	check(t, err)
	scope := o.NewCollectionScope(nil)
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		f, err := g.NewFlow(scopedInfo(tunnel, "unchanged.test"))
		if err != nil {
			return err
		}
		// When the replacement parent is invalid, the original remains usable.
		before := o.sequence
		if _, err := g.Reparent(f, foreignParent); !errors.Is(err, ErrClosed) {
			t.Fatal("foreign parent reparent accepted", err)
		}
		if f.closed || scope.flows[f.counter] != f || o.sequence != before || tunnel.children != 1 {
			t.Fatal("failed reparent damaged the original scope")
		}
		return g.Observe(f, 19, 7)
	}))
	check(t, scope.Close(normalEnd))
	check(t, tunnel.Close(normalEnd))
	check(t, foreignParent.Close(normalEnd))
}
