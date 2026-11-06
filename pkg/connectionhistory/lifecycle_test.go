package connectionhistory

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestConcurrentCallbacksAreIncludedExactlyOnce(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	tunnel, flow := owners(t, o)
	if err := tunnel.Close(normalEnd); !errors.Is(err, ErrChildrenLive) {
		t.Fatal("tunnel closed before children")
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var joined sync.WaitGroup
	joined.Add(2)
	go func() { defer joined.Done(); <-start; errs <- flow.Observe(19, 0) }()
	go func() { defer joined.Done(); <-start; errs <- flow.Observe(0, 7) }()
	close(start)
	joined.Wait()
	check(t, <-errs)
	check(t, <-errs)
	check(t, flow.Close(normalEnd))
	if err := flow.Close(normalEnd); !errors.Is(err, ErrClosed) {
		t.Fatal("duplicate terminal accepted")
	}
	if err := flow.Observe(1, 0); !errors.Is(err, ErrClosed) {
		t.Fatal("late data mutated terminal")
	}
	all := events(t, o, 0)
	end := all[len(all)-1]
	if *end.Bytes.Upload != 19 || *end.Bytes.Download != 7 {
		t.Fatal("joined callbacks omitted")
	}
}

func TestActivityWindowAndSharedCredentialQuality(t *testing.T) {
	o, err := NewObserver(Options{ProducerID: "wireguard:wg0", Runtime: "wireguard", RuntimeBuild: "qa", Capabilities: []string{"activity_window", "hot_toggle", "inner_ip_bytes"}})
	check(t, err)
	applyTest(t, o, 1, true, epochOne)
	tunnel, err := o.NewTunnel(TunnelInfo{Protocol: "wireguard", InboundTag: "wg0", Scope: "activity_window", PartialStart: true})
	check(t, err)
	f, err := o.NewFlow(FlowInfo{Tunnel: tunnel, Scope: "activity_window", Layer: "inner_ip_observed", PartialStart: true, Destination: nodeprov.HistoryDestination{Transport: "udp", ObservedIP: ptr("192.0.2.1"), ObservedPort: ptr(53)}, DestinationQuality: "observed"})
	check(t, err)
	check(t, f.Observe(64, 128))
	end := End{Reason: "idle_timeout", Semantics: "activity_expired"}
	check(t, f.Close(end))
	check(t, tunnel.Close(end))
	for _, e := range events(t, o, 0) {
		if e.FlowID != nil && (e.Quality.Start != "unknown" || e.Destination.RequestedHost != nil) {
			t.Fatal("invented packet start/domain")
		}
		if e.Kind == "flow_end" && e.Quality.End != "observed" {
			t.Fatal("activity endpoint claimed exact")
		}
	}
	shared := testObserver(t)
	b := nodeprov.HistoryBinding{Runtime: "xray", InboundTag: "ss", LabelKind: "shared_inbound", Label: "ss", CredentialRef: "33333333-3333-4333-8333-333333333333", Kind: "shared", BindingRevision: 1}
	_, err = shared.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: ptr(epochOne), Enabled: true, Bindings: []nodeprov.HistoryBinding{b}})
	check(t, err)
	_, err = shared.NewTunnel(TunnelInfo{Protocol: "shadowsocks", InboundTag: "ss", LabelKind: "shared_inbound", Label: "ss"})
	check(t, err)
	all := events(t, shared, 0)
	if all[len(all)-1].Auth.Kind != "shared" || all[len(all)-1].Quality.Identity != "unknown" {
		t.Fatal("shared secret was assigned to an account")
	}
}

func TestClockReversalPreservesSequenceAndMonotonicOrdering(t *testing.T) {
	o := testObserver(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	o.options.Clock = func() time.Time { return now }
	o.start = now
	applyTest(t, o, 1, true, epochOne)
	_, f := owners(t, o)
	now = now.Add(UpdateInterval)
	check(t, f.Observe(19, 7))
	check(t, o.Snapshot())
	now = now.Add(-time.Hour)
	check(t, f.Close(normalEnd))
	var seq, mono nodeprov.HistoryU64
	for _, e := range events(t, o, 0) {
		if e.Sequence <= seq || e.MonotonicNS < mono {
			t.Fatal("wall clock changed authoritative ordering")
		}
		seq, mono = e.Sequence, e.MonotonicNS
	}
}

func TestProductionReplayCapacityIsBoundedWithExplicitLoss(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	for range 2200 {
		tunnel, f := owners(t, o)
		check(t, f.Close(normalEnd))
		check(t, tunnel.Close(normalEnd))
		if o.queueBytes > ReplayCapacity {
			t.Fatal("8 MiB exceeded")
		}
	}
	all := events(t, o, 0)
	if all[0].Kind != "collection_gap" || all[0].Gap.Reason != "export_overflow" {
		t.Fatal("production capacity did not expose loss")
	}
}
