package connectionhistory

import (
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestDisableBoundaryOverflowRetainsLossAndStop(t *testing.T) {
	for _, scenario := range []string{"first_loss", "coalesced_loss", "acked_prefix", "exact_fit"} {
		t.Run(scenario, func(t *testing.T) {
			// Given a quiescent exporter with only completed owners in replay.
			o := testObserver(t)
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			o.options.Clock = func() time.Time {
				now = now.Add(time.Second)
				return now
			}
			o.start = now
			applyTest(t, o, 7, true, epochOne)
			if scenario == "coalesced_loss" {
				o.capacity = 5000
				for range 8 {
					tunnel, flow := owners(t, o)
					check(t, flow.Close(normalEnd))
					check(t, tunnel.Close(normalEnd))
				}
				if o.loss == nil {
					t.Fatal("fixture did not create previous overflow")
				}
				o.capacity = ReplayCapacity
			}
			for range 2 {
				tunnel, flow := owners(t, o)
				check(t, flow.Observe(19, 7))
				check(t, flow.Close(normalEnd))
				check(t, tunnel.Close(normalEnd))
			}
			if scenario == "acked_prefix" {
				check(t, o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.boot, ThroughSequence: 5}))
			}
			if len(o.flows) != 0 || len(o.tunnels) != 0 {
				t.Fatal("fixture has live owners")
			}
			before := o.sequence
			first := o.frames[0].sequence
			if o.loss != nil {
				first = *o.loss.FirstSequence
			}
			boundary := o.baseLocked("collection_boundary")
			boundary.ConfigRevision = 8
			boundary.Sequence, boundary.EventID = before+1, string(o.boot)+":"+(before+1).String()
			boundary.Boundary = &nodeprov.HistoryBoundary{Action: "disabled", PriorRevision: 7}
			line, err := nodeprov.EncodeHistoryEvent(boundary)
			check(t, err)
			o.capacity = o.queueBytes + len(line)
			if scenario != "exact_fit" {
				o.capacity--
			}
			t.Logf("BOUNDARY before=%s queued=%d boundary=%d capacity=%d owners=0", before, o.queueBytes, len(line), o.capacity)

			// When the disabled boundary itself reaches the replay byte limit.
			off := applyTest(t, o, 8, false, epochOne)

			// Then a valid active-revision gap precedes the retained stop barrier.
			all := events(t, o, o.acked)
			for _, event := range all {
				t.Logf("REPLAY sequence=%s kind=%s revision=%s gap=%+v boundary=%+v", event.Sequence, event.Kind, event.ConfigRevision, event.Gap, event.Boundary)
			}
			if scenario != "exact_fit" {
				if len(all) != 2 {
					t.Fatalf("overflow must retain loss then stop, got %d frames; applied=%+v", len(all), off)
				}
				gap := all[0]
				if gap.Gap == nil || gap.Gap.Reason != "export_overflow" || gap.ConfigRevision != 7 ||
					gap.CollectionEpoch != epochOne || *gap.Gap.FirstSequence != first ||
					*gap.Gap.LastSequence != gap.Sequence-1 || *gap.Gap.LostEvents != gap.Sequence-first ||
					*gap.Gap.LastSequence < before || all[1].Sequence != gap.Sequence+1 ||
					all[1].MonotonicNS < gap.MonotonicNS || all[1].EventTime < gap.EventTime {
					t.Fatalf("invalid ordered loss: %+v", gap)
				}
				expectControlError(t, o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.boot, ThroughSequence: before}), "export_unavailable")
			}
			stop := all[len(all)-1]
			if stop.Boundary == nil || stop.Boundary.Action != "disabled" || stop.Boundary.PriorRevision != 7 ||
				stop.ConfigRevision != 8 || stop.CollectionEpoch != epochOne ||
				stop.Sequence != off.BarrierSequence || off.Enabled || o.queueBytes > o.capacity {
				t.Fatalf("unusable stop barrier: event=%+v applied=%+v", stop, off)
			}
			again := applyTest(t, o, 8, false, epochOne)
			if again.BarrierSequence != off.BarrierSequence {
				t.Fatal("idempotent disable changed its barrier")
			}
			check(t, o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.boot, ThroughSequence: off.BarrierSequence}))
			check(t, o.Snapshot())
			if o.Hello().Enabled || o.Hello().LastSequence != off.BarrierSequence ||
				len(events(t, o, off.BarrierSequence)) != 0 || o.queueBytes != 0 {
				t.Fatal("OFF replay did not drain or emitted new detail")
			}
		})
	}
}
