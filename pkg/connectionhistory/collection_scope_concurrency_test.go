package connectionhistory

import (
	"errors"
	"sync"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestCollectionScopeConcurrentObservationDisableAndClose(t *testing.T) {
	for range 60 {
		o := testObserver(t)
		applyTest(t, o, 1, true, epochOne)
		tunnel, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "scoped"})
		check(t, err)
		flows := make(map[string]*Flow)
		resets := 0
		scope := o.NewCollectionScope(func() { clear(flows); resets++ })
		start := make(chan struct{})
		errs := make(chan error, 3)
		var joined sync.WaitGroup
		joined.Add(3)
		go func() {
			defer joined.Done()
			<-start
			errs <- scope.WithEnabled(func(g *CollectionGate) error {
				f, err := g.NewFlow(scopedInfo(tunnel, "concurrent.test"))
				if err != nil {
					return err
				}
				flows["concurrent.test"] = f
				return g.Observe(f, 19, 7)
			})
		}()
		go func() {
			defer joined.Done()
			<-start
			_, err := o.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 2, Enabled: false,
				CollectionEpoch: ptr(epochOne), Bindings: []nodeprov.HistoryBinding{}})
			errs <- err
		}()
		go func() {
			defer joined.Done()
			<-start
			errs <- scope.Close(normalEnd)
		}()
		close(start)
		joined.Wait()
		for range 3 {
			if err := <-errs; err != nil && !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
		}
		if !scope.closed || len(o.scopes) != 0 || len(scope.flows) != 0 ||
			len(o.flows) != 0 || len(flows) != 0 || tunnel.children != 0 || resets < 1 || resets > 2 {
			t.Fatal("scope/drop race retained destinations or parent counts")
		}
		var disabled bool
		var terminals int
		for _, e := range events(t, o, 0) {
			if disabled {
				t.Fatal("scoped callback emitted after disabled boundary")
			}
			if e.Kind == "collection_boundary" && e.Boundary.Action == "disabled" {
				disabled = true
			}
			if e.Kind == "flow_update" || e.Kind == "flow_end" {
				if *e.Bytes.Upload != 19 || *e.Bytes.Download != 7 {
					t.Fatal("gate split destination creation from observation")
				}
				if e.Kind == "flow_end" {
					terminals++
				}
			}
		}
		if !disabled || terminals > 1 {
			t.Fatal("missing boundary or duplicate scoped terminal")
		}
	}
}

func TestCollectionScopeCreatedOFFStartsPartialOnFirstObservation(t *testing.T) {
	o := testObserver(t)
	tunnel, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "scoped"})
	check(t, err)
	scope := o.NewCollectionScope(nil)
	applyTest(t, o, 1, true, epochOne)
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		f, err := g.NewFlow(scopedInfo(tunnel, "first-on.test"))
		if err != nil {
			return err
		}
		return g.Observe(f, 19, 7)
	}))
	all := events(t, o, 0)
	e := all[len(all)-1]
	if e.Kind != "flow_start" || !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil ||
		*e.Bytes.Upload != 0 || *e.Bytes.Download != 0 {
		t.Fatal("OFF association acquired a fabricated exact start")
	}
	check(t, scope.Close(normalEnd))
}

func TestCollectionScopeReparentRemainsTrackedForDisable(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	oldParent, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "old"})
	check(t, err)
	newParent, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "new"})
	check(t, err)
	scope := o.NewCollectionScope(nil)
	var first *Flow
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		var err error
		first, err = g.NewFlow(scopedInfo(oldParent, "migrating.test"))
		return err
	}))
	next, err := first.Reparent(newParent)
	check(t, err)
	if len(scope.flows) != 1 || scope.flows[next.counter] != next {
		t.Fatal("reparent escaped collection scope")
	}
	applyTest(t, o, 2, false, epochOne)
	if len(o.flows) != 0 || oldParent.children != 0 || newParent.children != 0 || !next.closed {
		t.Fatal("disable failed to retire reparented scoped flow")
	}
	check(t, scope.Close(normalEnd))
}

func TestCollectionScopeCloseReleasesTrackingOnFinalizationError(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	scope := o.NewCollectionScope(nil)
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		info := scopedInfo(nil, "error.test")
		info.Protocol = "vless"
		_, err := g.NewFlow(info)
		return err
	}))
	if err := scope.Close(End{}); !errors.Is(err, nodeprov.ErrHistoryInvalid) {
		t.Fatal("invalid end was not reported")
	}
	if !scope.closed || len(o.scopes) != 0 || len(o.flows) != 0 || len(scope.flows) != 0 {
		t.Fatal("failed finalization retained association history")
	}
	for _, e := range events(t, o, 0) {
		if e.Kind == "flow_end" {
			t.Fatal("failed finalization fabricated an end")
		}
	}
}

func TestCollectionGatePartialStartTracksScopeCreationAndDisable(t *testing.T) {
	for _, createdON := range []bool{false, true} {
		o := testObserver(t)
		if createdON {
			applyTest(t, o, 1, true, epochOne)
		}
		scope := o.NewCollectionScope(nil)
		if !createdON {
			applyTest(t, o, 1, true, epochOne)
		}
		check(t, scope.WithEnabled(func(g *CollectionGate) error {
			if g.PartialStart() == createdON {
				t.Fatal("partial-start accessor disagrees with association creation")
			}
			return nil
		}))
		applyTest(t, o, 2, false, epochOne)
		applyTest(t, o, 3, true, epochTwo)
		check(t, scope.WithEnabled(func(g *CollectionGate) error {
			if !g.PartialStart() {
				t.Fatal("surviving association forgot the OFF interval")
			}
			return nil
		}))
		check(t, scope.Close(normalEnd))
	}
}

func TestCollectionGatePartialStartTracksAssociationLifetime(t *testing.T) {
	for _, initiallyOFF := range []bool{false, true} {
		o := testObserver(t)
		if !initiallyOFF {
			applyTest(t, o, 1, true, epochOne)
		}
		scope := o.NewCollectionScope(nil)
		if initiallyOFF {
			applyTest(t, o, 1, true, epochOne)
		}
		check(t, scope.WithEnabled(func(g *CollectionGate) error {
			if g.PartialStart() != initiallyOFF {
				t.Fatal("partial start does not reflect initial collection state")
			}
			return nil
		}))
		applyTest(t, o, 2, false, epochOne)
		applyTest(t, o, 3, true, epochTwo)
		check(t, scope.WithEnabled(func(g *CollectionGate) error {
			if !g.PartialStart() {
				t.Fatal("crossed-OFF association claims exact start")
			}
			return nil
		}))
		check(t, scope.Close(normalEnd))
	}
}

func TestCollectionScopeConcurrentReparentAndDisable(t *testing.T) {
	for range 40 {
		o := testObserver(t)
		applyTest(t, o, 1, true, epochOne)
		oldParent, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "old"})
		check(t, err)
		newParent, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "new"})
		check(t, err)
		scope := o.NewCollectionScope(nil)
		var first *Flow
		check(t, scope.WithEnabled(func(g *CollectionGate) error {
			var err error
			first, err = g.NewFlow(scopedInfo(oldParent, "handoff.test"))
			return err
		}))
		start := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			<-start
			_, err := first.Reparent(newParent)
			result <- err
		}()
		close(start)
		off := applyTest(t, o, 2, false, epochOne)
		if err := <-result; err != nil && !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		if len(scope.flows) != 0 || len(o.flows) != 0 || oldParent.children != 0 || newParent.children != 0 {
			t.Fatal("reparent/disable retained a scoped owner")
		}
		if len(events(t, o, off.BarrierSequence)) != 0 {
			t.Fatal("reparent emitted after the OFF barrier")
		}
		check(t, scope.Close(normalEnd))
		check(t, oldParent.Close(normalEnd))
		check(t, newParent.Close(normalEnd))
	}
}
