package connectionhistory

import (
	"bytes"
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func binding() nodeprov.HistoryBinding {
	return nodeprov.HistoryBinding{
		Runtime: "xray", InboundTag: "vless", LabelKind: "xray_email", Label: "configured@example.test",
		CredentialRef: "33333333-3333-4333-8333-333333333333", Kind: "device", BindingRevision: 1,
	}
}
func expectControlError(t *testing.T, err error, code string) {
	t.Helper()
	var ce *nodeprov.HistoryError
	if !errors.As(err, &ce) || ce.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestApplyRevisionEpochAndBindingRules(t *testing.T) {
	o := testObserver(t)
	a := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 7, Enabled: true, CollectionEpoch: ptr(epochOne), Bindings: []nodeprov.HistoryBinding{binding()}}
	first, err := o.Apply(a)
	check(t, err)
	again, err := o.Apply(a)
	check(t, err)
	if first.BarrierSequence != again.BarrierSequence {
		t.Fatal("idempotent apply emitted events")
	}
	a.ConfigRevision = 6
	_, err = o.Apply(a)
	expectControlError(t, err, "revision_stale")
	a.ConfigRevision = 7
	a.Bindings = []nodeprov.HistoryBinding{}
	_, err = o.Apply(a)
	expectControlError(t, err, "revision_conflict")
	a.ConfigRevision = 8
	a.CollectionEpoch = ptr(epochTwo)
	_, err = o.Apply(a)
	expectControlError(t, err, "epoch_invalid")
	applyTest(t, o, 8, false, epochOne)
	_, err = o.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 9, Enabled: true, CollectionEpoch: ptr(epochOne), Bindings: []nodeprov.HistoryBinding{}})
	expectControlError(t, err, "epoch_invalid")
	a.ConfigRevision = 9
	a.Bindings = []nodeprov.HistoryBinding{binding(), binding()}
	_, err = o.Apply(a)
	expectControlError(t, err, "binding_invalid")
}

func TestCredentialBindingPinnedAndNeverExportsLabels(t *testing.T) {
	o := testObserver(t)
	_, held := owners(t, o)
	a := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, Enabled: true, CollectionEpoch: ptr(epochOne), Bindings: []nodeprov.HistoryBinding{binding()}}
	_, err := o.Apply(a)
	check(t, err)
	b := binding()
	b.CredentialRef = "55555555-5555-4555-8555-555555555555"
	b.BindingRevision = 2
	a.ConfigRevision, a.Bindings = 2, []nodeprov.HistoryBinding{b}
	_, err = o.Apply(a)
	check(t, err)
	_, newFlow := owners(t, o)
	check(t, held.Close(normalEnd))
	check(t, newFlow.Close(normalEnd))
	frames, err := o.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.boot})
	check(t, err)
	for _, line := range frames {
		if bytes.Contains(line, []byte(binding().Label)) {
			t.Fatal("private auth label exported")
		}
		e, err := nodeprov.DecodeHistoryEvent(line)
		check(t, err)
		if e.FlowID == nil {
			continue
		}
		want := binding()
		if *e.FlowID == newFlow.ID() {
			want = b
		}
		if e.Auth.CredentialRef == nil || *e.Auth.CredentialRef != want.CredentialRef || *e.Auth.BindingRevision != want.BindingRevision {
			t.Fatal("flow identity reassigned")
		}
	}
}

func TestReplayAckAndOverflowGap(t *testing.T) {
	o := testObserver(t)
	// Small private test capacity exercises the production overflow algorithm;
	// production always uses the exported 8 MiB bound.
	o.capacity = 5000
	applyTest(t, o, 1, true, epochOne)
	for range 8 {
		tunnel, f := owners(t, o)
		check(t, f.Observe(19, 7))
		check(t, f.Close(normalEnd))
		check(t, tunnel.Close(normalEnd))
	}
	all := events(t, o, 0)
	if len(all) == 0 || all[0].Gap == nil {
		t.Fatal("replay has unexplained hole")
	}
	gap := all[0]
	if *gap.Gap.FirstSequence != 1 || *gap.Gap.LastSequence != gap.Sequence-1 || *gap.Gap.LostEvents != gap.Sequence-1 {
		t.Fatalf("incorrect coalesced loss: %+v", gap.Gap)
	}
	if o.queueBytes > o.capacity {
		t.Fatal("unbounded queue")
	}
	err := o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.boot, ThroughSequence: 1})
	expectControlError(t, err, "export_unavailable")
	last := all[len(all)-1].Sequence
	check(t, o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.boot, ThroughSequence: last}))
	check(t, o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.boot, ThroughSequence: last}))
	if len(events(t, o, last)) != 0 || o.queueBytes != 0 {
		t.Fatal("ACK did not reclaim replay")
	}
	_, err = o.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.boot, AfterSequence: 0})
	expectControlError(t, err, "export_unavailable")
}

func TestDestinationRefinementKeepsOriginalRequest(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	_, f := owners(t, o)
	effective := "redirect.test"
	peer := nodeprov.HistoryPeer{IP: "192.0.2.44", Port: 8443}
	check(t, f.SetDestination(&effective, &peer))
	effective, peer.IP = "mutated", "198.51.100.1"
	check(t, f.Close(normalEnd))
	all := events(t, o, 0)
	d := all[len(all)-1].Destination
	if *d.RequestedHost != "example.test" || *d.RequestedPort != 443 || *d.EffectiveHost != "redirect.test" || *d.ObservedIP != "192.0.2.44" || *d.ObservedPort != 8443 {
		t.Fatalf("destination: %+v", d)
	}
}

func TestReportGapDegradesFinalityAndOffIsSilent(t *testing.T) {
	o := testObserver(t)
	applyTest(t, o, 1, true, epochOne)
	_, f := owners(t, o)
	check(t, f.Observe(19, 7))
	g := nodeprov.HistoryGap{ProducerID: "xray", ObserverBootID: o.boot, Reason: "capture_drop"}
	check(t, o.ReportGap(g))
	check(t, f.Close(normalEnd))
	all := events(t, o, 0)
	e := all[len(all)-1]
	if e.Bytes.Final || e.Quality.Completeness != "unknown" || *e.Bytes.Upload != 19 {
		t.Fatal("gap concealed by finality")
	}
	off := applyTest(t, o, 2, false, epochOne)
	check(t, o.ReportGap(g))
	if o.Hello().LastSequence != off.BarrierSequence {
		t.Fatal("OFF gap callback emitted")
	}
}
