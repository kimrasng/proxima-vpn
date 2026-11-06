package connectionhistory

import (
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestReparentSerializesSegmentBoundaryWithDisable(t *testing.T) {
	for range 30 {
		o := testObserver(t)
		applyTest(t, o, 1, true, epochOne)
		oldParent, f := owners(t, o)
		newParent, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "vless"})
		check(t, err)
		check(t, f.Observe(19, 7))
		start := make(chan struct{})
		rotated := make(chan *Flow, 1)
		errs := make(chan error, 2)
		go func() { <-start; next, err := f.Reparent(newParent); rotated <- next; errs <- err }()
		go func() {
			<-start
			_, err := o.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 2, Enabled: false, CollectionEpoch: ptr(epochOne), Bindings: []nodeprov.HistoryBinding{}})
			errs <- err
		}()
		close(start)
		check(t, <-errs)
		check(t, <-errs)
		next := <-rotated
		if next.ID() == f.ID() {
			t.Fatal("segment ID reused")
		}
		if err := f.Observe(1, 0); !errors.Is(err, ErrClosed) {
			t.Fatal("late callback accepted")
		}
		check(t, oldParent.Close(normalEnd))
		all := events(t, o, 0)
		for i, e := range all {
			if e.Kind == "flow_end" && *e.FlowID == f.ID() {
				if i+1 == len(all) || all[i+1].Kind != "flow_start" || *all[i+1].FlowID != next.ID() {
					t.Fatal("control interleaved segment pair")
				}
				if *e.Bytes.Upload != 19 || *e.Bytes.Download != 7 || *e.Lifecycle.EndSemantics != "observation_segment_closed" {
					t.Fatal("lost segment counters")
				}
				if *all[i+1].Bytes.Upload != 0 || *all[i+1].TunnelID != newParent.ID() {
					t.Fatal("new segment baseline/parent")
				}
			}
		}
		check(t, next.Close(normalEnd))
		check(t, newParent.Close(normalEnd))
		if len(o.flows) != 0 || len(o.tunnels) != 0 || next.info.Destination.RequestedHost != nil {
			t.Fatal("completed OFF owner retained")
		}
	}
}

func TestInvalidReparentLeavesOldSegmentOpen(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	_, f := owners(t, o)
	other := testObserver(t)
	tunnel, err := other.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "elsewhere"})
	check(t, err)
	if _, err := f.Reparent(tunnel); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	check(t, f.Observe(19, 7))
	check(t, f.Close(normalEnd))
	all := events(t, o, 0)
	if *all[len(all)-1].Bytes.Upload != 19 {
		t.Fatal("failed reparent damaged old owner")
	}
}
