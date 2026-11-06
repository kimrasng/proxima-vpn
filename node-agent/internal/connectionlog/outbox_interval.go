package connectionlog

import (
	"fmt"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// The last durable observation interval is independent of the newest hello or
// applied policy. In particular, a disabling revision cannot authorize a gap.
type collectionInterval struct {
	Epoch    nodeprov.HistoryUUID
	Revision nodeprov.HistoryU64
	ClosedAt *nodeprov.HistoryTime
}

func eventInterval(e nodeprov.HistoryEvent) *collectionInterval {
	interval := &collectionInterval{Epoch: e.CollectionEpoch, Revision: e.ConfigRevision}
	if e.Boundary != nil && e.Boundary.Action == "disabled" {
		interval.Revision, interval.ClosedAt = e.Boundary.PriorRevision, &e.EventTime
	}
	return interval
}

func (cp checkpoint) interval() *collectionInterval {
	if cp.Interval != nil {
		return cp.Interval
	}
	if cp.Epoch == nil {
		return nil
	}
	// Older checkpoints did not retain the active revision across OFF. A
	// durable applied OFF still proves absence of later runtime collection.
	interval := &collectionInterval{Epoch: *cp.Epoch}
	if cp.Applied != nil && !cp.Applied.Enabled && cp.Through >= cp.Applied.BarrierSequence {
		interval.ClosedAt = cp.Time
	} else if cp.Hello.Enabled {
		interval.Revision = cp.Hello.ConfigRevision
	} else if cp.Applied != nil && cp.Applied.Enabled {
		interval.Revision = cp.Applied.ConfigRevision
	}
	return interval
}

func (o *outbox) noteLoss(id string, cp checkpoint, reason string) error {
	interval := cp.interval()
	if interval == nil {
		return nil
	}
	if interval.ClosedAt != nil && (reason == "producer_lost" || reason == "observer_restart") {
		return nil // A durably closed interval cannot lose new observations.
	}
	if interval.Revision == 0 {
		// Retain unprovable queued damage rather than deleting it or emitting
		// an invalid disabling-revision gap. Retained boundaries recover this
		// revision for old checkpoints when their segments are still present.
		return fmt.Errorf("history loss for %s has no proven enabled revision", id)
	}
	key := id + "/" + string(cp.Hello.ObserverBootID) + "/" + string(interval.Epoch)
	if _, exists := o.disk.Losses[key]; exists {
		return nil
	}
	from := cp.Time
	if interval.ClosedAt != nil {
		from = nil // Queued damage can precede the last durable OFF boundary.
	}
	o.disk.Losses[key] = lossState{
		Gap: nodeprov.HistoryGap{ProducerID: id, ObserverBootID: cp.Hello.ObserverBootID,
			FromTime: from, ToTime: interval.ClosedAt, Reason: reason},
		Epoch: interval.Epoch, Revision: interval.Revision,
	}
	return nil
}
