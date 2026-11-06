package connectionhistory

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

const epochOne nodeprov.HistoryUUID = "22222222-2222-4222-8222-222222222222"
const epochTwo nodeprov.HistoryUUID = "44444444-4444-4444-8444-444444444444"

func testObserver(t *testing.T) *Observer {
	t.Helper()
	o, err := NewObserver(Options{ProducerID: "xray", Runtime: "xray", RuntimeBuild: "fixture", Capabilities: []string{"hot_toggle", "tcp_flow"}})
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func applyTest(t *testing.T, o *Observer, revision uint64, enabled bool, epoch nodeprov.HistoryUUID) nodeprov.HistoryApplied {
	t.Helper()
	a, err := o.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: nodeprov.HistoryU64(revision), Enabled: enabled, CollectionEpoch: ptr(epoch), Bindings: []nodeprov.HistoryBinding{}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func owners(t *testing.T, o *Observer) (*Tunnel, *Flow) {
	t.Helper()
	tunnel, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "vless", LabelKind: "xray_email", Label: "configured@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := o.NewFlow(FlowInfo{Tunnel: tunnel, Destination: nodeprov.HistoryDestination{RequestedHost: ptr("example.test"), RequestedPort: ptr(443), Transport: "tcp"}})
	if err != nil {
		t.Fatal(err)
	}
	return tunnel, flow
}
func events(t *testing.T, o *Observer, after nodeprov.HistoryU64) []nodeprov.HistoryEvent {
	t.Helper()
	frames, err := o.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.boot, AfterSequence: after})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]nodeprov.HistoryEvent, 0, len(frames))
	for _, line := range frames {
		e, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		out = append(out, e)
	}
	return out
}
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var normalEnd = End{Reason: "eof", Semantics: "local_owner_closed"}

func TestCumulativeSnapshotsAndThrottle(t *testing.T) {
	// Given an injectable clock, separate from wall-clock scheduling.
	o := testObserver(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	o.options.Clock = func() time.Time { return now }
	o.start = now
	applyTest(t, o, 1, true, epochOne)
	tunnel, flow := owners(t, o)
	check(t, flow.Observe(12, 3))
	check(t, o.Snapshot())
	if len(events(t, o, 0)) != 3 {
		t.Fatal("early periodic update")
	}
	now = now.Add(UpdateInterval)
	check(t, o.Snapshot())
	check(t, flow.Observe(7, 4))
	check(t, flow.Close(normalEnd))
	check(t, tunnel.Close(End{Reason: "transport_close", Semantics: "local_owner_closed"}))
	// Then start/update/end are snapshots 0/0, 12/3, 19/7, not deltas.
	var got []nodeprov.HistoryEvent
	for _, e := range events(t, o, 0) {
		if e.FlowID != nil {
			got = append(got, e)
		}
	}
	if len(got) != 3 {
		t.Fatalf("flow snapshots: %d", len(got))
	}
	for i, want := range [][2]nodeprov.HistoryU64{{0, 0}, {12, 3}, {19, 7}} {
		if *got[i].Bytes.Upload != want[0] || *got[i].Bytes.Download != want[1] {
			t.Fatalf("snapshot %d: %+v", i, got[i].Bytes)
		}
	}
	if !got[2].Bytes.Final || got[2].Lifecycle.EndedAt == nil {
		t.Fatal("missing terminal snapshot")
	}
}

func TestHeldOwnersAcrossEpochs(t *testing.T) {
	o := testObserver(t)
	tunnel, flow := owners(t, o)
	check(t, flow.Observe(900, 900))
	applyTest(t, o, 1, true, epochOne)
	check(t, flow.Observe(12, 3))
	off := applyTest(t, o, 2, false, epochOne)
	check(t, flow.Observe(500, 500))
	check(t, o.Snapshot())
	if got := o.Hello().LastSequence; got != off.BarrierSequence {
		t.Fatal("post-disable detail")
	}
	applyTest(t, o, 3, true, epochTwo)
	check(t, flow.Observe(19, 7))
	check(t, flow.Close(normalEnd))
	check(t, tunnel.Close(normalEnd))
	var starts, ends int
	for _, e := range events(t, o, 0) {
		if e.Kind == "flow_start" {
			starts++
			if *e.FlowID != flow.ID() || !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil || *e.Bytes.Upload != 0 || *e.Bytes.Download != 0 {
				t.Fatal("partial baseline")
			}
		}
		if e.Kind == "flow_update" && e.CollectionEpoch == epochOne {
			if *e.Bytes.Upload != 12 || *e.Bytes.Download != 3 || e.Bytes.Final || e.Lifecycle.EndedAt != nil {
				t.Fatal("disable fabricated end")
			}
		}
		if e.Kind == "flow_end" {
			ends++
			if e.CollectionEpoch != epochTwo || *e.Bytes.Upload != 19 || *e.Bytes.Download != 7 {
				t.Fatal("epoch totals leaked")
			}
		}
	}
	if starts != 2 || ends != 1 {
		t.Fatalf("starts/ends %d/%d", starts, ends)
	}
}

func TestConcurrentGateAndClose(t *testing.T) {
	for range 40 {
		o := testObserver(t)
		applyTest(t, o, 1, true, epochOne)
		tunnel, flow := owners(t, o)
		check(t, flow.Observe(19, 7))
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; results <- flow.Close(normalEnd) }()
		go func() {
			defer wg.Done()
			<-start
			_, err := o.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 2, Enabled: false, CollectionEpoch: ptr(epochOne), Bindings: []nodeprov.HistoryBinding{}})
			results <- err
		}()
		close(start)
		wg.Wait()
		check(t, <-results)
		check(t, <-results)
		barrier := o.Hello().LastSequence
		check(t, tunnel.Close(normalEnd))
		if o.Hello().LastSequence != barrier {
			t.Fatal("terminal after disable barrier")
		}
		var terminal int
		for _, e := range events(t, o, 0) {
			if e.Kind == "flow_end" {
				terminal++
			}
			if e.Kind == "flow_end" && (*e.Bytes.Upload != 19 || *e.Bytes.Download != 7) {
				t.Fatal("partial final")
			}
		}
		if terminal > 1 {
			t.Fatal("duplicate terminal")
		}
	}
}

func TestCounterOverflowDoesNotWrapOrClaimFinal(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	_, f := owners(t, o)
	check(t, f.Observe(math.MaxUint64, 7))
	if err := f.Observe(1, 0); !errors.Is(err, ErrCounterOverflow) {
		t.Fatal(err)
	}
	check(t, f.Close(normalEnd))
	all := events(t, o, 0)
	if all[len(all)-2].Gap.Reason != "counter_overflow" {
		t.Fatal("missing counter gap")
	}
	end := all[len(all)-1]
	if end.Bytes.Upload != nil || end.Bytes.Download != nil || end.Bytes.Final || end.Quality.Completeness != "unknown" {
		t.Fatal("overflow claimed exact final")
	}
}
