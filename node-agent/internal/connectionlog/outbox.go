package connectionlog

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func (o *outbox) recoverRecords() error {
	entries, err := os.ReadDir(filepath.Join(o.dir, "records"))
	if err != nil {
		return err
	}
	for i, entry := range entries {
		path := filepath.Join(o.dir, "records", entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmp") {
			for id, cp := range o.disk.Sources {
				o.noteLoss(id, cp, "disk_error")
			}
			if err := o.saveState(); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".jsonl"), 10, 64)
		if err != nil || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return fmt.Errorf("unexpected history record %q", entry.Name())
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		e, decodeErr := nodeprov.DecodeHistoryEvent(b)
		if decodeErr != nil {
			if i != len(entries)-1 || (len(b) > 0 && b[len(b)-1] == '\n') {
				return fmt.Errorf("corrupt committed history record %s: %w", entry.Name(), decodeErr)
			}
			for id, cp := range o.disk.Sources {
				o.noteLoss(id, cp, "disk_error")
			}
			if err := o.saveState(); err != nil {
				return err
			}
			// One event per append-only segment: the last committed line is offset zero.
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			err = errors.Join(f.Truncate(0), o.sync(f), f.Close())
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		o.bytes += recordCharge(int64(len(b)))
		if n >= o.disk.Next {
			o.advance(e)
			o.disk.Next = n + 1
		}
	}
	if err := o.syncDir(filepath.Join(o.dir, "records")); err != nil {
		return err
	}
	if err := o.saveState(); err != nil {
		return err
	}
	return o.recoverBatch()
}

func (o *outbox) advance(e nodeprov.HistoryEvent) {
	if e.Gap != nil && e.Gap.LostEvents != nil {
		n := *e.Gap.LostEvents
		if math.MaxUint64-o.disk.Dropped < n {
			o.disk.Dropped = math.MaxUint64
		} else {
			o.disk.Dropped += n
		}
	}
	if e.ProducerID == "agent" {
		if e.ObserverBootID == o.disk.AgentBoot && e.Sequence > o.disk.AgentSeq {
			o.disk.AgentSeq = e.Sequence
		}
		return
	}
	cp := o.disk.Sources[e.ProducerID]
	if cp.Hello.ObserverBootID != e.ObserverBootID {
		cp = checkpoint{Hello: nodeprov.HistoryHello{ProducerID: e.ProducerID, ObserverBootID: e.ObserverBootID}}
	}
	if e.Boundary != nil && e.Boundary.Action == "enabled" {
		cp.GapCode = ""
	}
	if e.Gap != nil && e.Gap.ProducerID == e.ProducerID && e.Gap.ObserverBootID == e.ObserverBootID {
		cp.GapCode = e.Gap.Reason
	}
	cp.Through, cp.Time, cp.Epoch = e.Sequence, &e.EventTime, &e.CollectionEpoch
	cp.Hello.ConfigRevision = e.ConfigRevision
	o.disk.Sources[e.ProducerID] = cp
}

func (o *outbox) appendEvent(e nodeprov.HistoryEvent) error {
	b, err := nodeprov.EncodeHistoryEvent(e)
	if err != nil {
		return err
	}
	if o.bytes+recordCharge(int64(len(b)))+ReservedBytes+2*int64(1<<20) > o.limit {
		return ErrOutboxFull
	}
	path := filepath.Join(o.dir, "records", fmt.Sprintf("%020d.jsonl", o.disk.Next))
	if err := o.publish(path, b); err != nil {
		return err
	}
	o.bytes += recordCharge(int64(len(b)))
	o.disk.Next++
	o.advance(e)
	return o.saveState()
}

// Charge small segments for allocation granularity plus directory bookkeeping,
// not only JSON bytes. The two-MiB batch allowance is outside the loss reserve.
func recordCharge(n int64) int64 { return ((n+4095)/4096)*4096 + 256 }

func (o *outbox) accept(e nodeprov.HistoryEvent) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	cp, ok := o.disk.Sources[e.ProducerID]
	if !ok || cp.Hello.ObserverBootID != e.ObserverBootID {
		return fmt.Errorf("event does not match registered hello")
	}
	if e.Sequence <= cp.Through {
		// A prior checkpoint sync may have failed. Never turn that failure into an ACK.
		return o.saveState()
	}
	if e.Sequence != cp.Through+1 {
		g := e.Gap
		if g == nil || g.ProducerID != e.ProducerID || g.ObserverBootID != e.ObserverBootID ||
			g.FirstSequence == nil || *g.FirstSequence > cp.Through+1 ||
			g.LastSequence == nil || *g.LastSequence != e.Sequence-1 {
			return fmt.Errorf("unexplained history sequence hole after %s", cp.Through)
		}
	}
	if err := o.flushLosses(); err != nil {
		return err
	}
	if err := o.appendEvent(e); err != nil {
		reason := "disk_error"
		if errors.Is(err, ErrOutboxFull) {
			reason = "outbox_full"
		}
		cp.Epoch = &e.CollectionEpoch
		cp.Hello.ConfigRevision = e.ConfigRevision
		o.noteLoss(e.ProducerID, cp, reason)
		return errors.Join(err, o.saveState())
	}
	return nil
}

func (o *outbox) noteLoss(id string, cp checkpoint, reason string) {
	if cp.Epoch == nil {
		return
	}
	if _, exists := o.disk.Losses[id]; exists {
		return // Coalesce an unknown interval from its earliest durable observation.
	}
	o.disk.Losses[id] = lossState{
		Gap: nodeprov.HistoryGap{ProducerID: id, ObserverBootID: cp.Hello.ObserverBootID,
			FromTime: cp.Time, Reason: reason},
		Epoch: *cp.Epoch, Revision: cp.Hello.ConfigRevision,
	}
}

func (o *outbox) flushLosses() error {
	keys := make([]string, 0, len(o.disk.Losses))
	for id := range o.disk.Losses {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		loss := o.disk.Losses[id]
		now := nodeprov.HistoryTimestamp(o.clock())
		loss.Gap.ToTime = &now
		seq := o.disk.AgentSeq + 1
		e := nodeprov.HistoryEvent{
			SchemaVersion: 1, ObserverBootID: o.disk.AgentBoot, Sequence: seq,
			EventID:    string(o.disk.AgentBoot) + ":" + seq.String(),
			ProducerID: "agent", Runtime: "agent", RuntimeBuild: "node-agent-history-v1",
			ConfigRevision: loss.Revision, CollectionEpoch: loss.Epoch, EventTime: now,
			Kind: "collection_gap", Auth: nodeprov.HistoryAuth{Kind: "unknown"},
			Bytes: nodeprov.HistoryBytes{Layer: "none"}, Lifecycle: nodeprov.HistoryLifecycle{Scope: "collection"},
			Quality: nodeprov.HistoryQuality{Identity: "unknown", Parent: "unknown", Destination: "unknown",
				Bytes: "unknown", Start: "unknown", End: "unknown", Completeness: "unknown"}, Gap: &loss.Gap,
		}
		if err := o.appendEvent(e); err != nil {
			return err
		}
		delete(o.disk.Losses, id)
		if err := o.saveState(); err != nil {
			return err
		}
	}
	return nil
}

func (o *outbox) hello(h nodeprov.HistoryHello) (nodeprov.HistoryU64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	old, exists := o.disk.Sources[h.ProducerID]
	if !exists && len(o.disk.Sources) >= maxProducers {
		return 0, fmt.Errorf("too many history producers")
	}
	cp := old
	if exists && o.unclean && old.Epoch == nil && h.CollectionEpoch != nil {
		lost := old
		lost.Epoch, lost.Hello.ConfigRevision = h.CollectionEpoch, h.ConfigRevision
		o.noteLoss(h.ProducerID, lost, "producer_lost")
	}
	if cp.Hello.ObserverBootID != h.ObserverBootID {
		if exists {
			o.noteLoss(h.ProducerID, old, "observer_restart")
		}
		cp = checkpoint{}
	}
	cp.Hello = h
	if cp.Epoch == nil {
		cp.Epoch = h.CollectionEpoch
	}
	o.disk.Sources[h.ProducerID] = cp
	if err := o.saveState(); err != nil {
		return 0, err
	}
	if err := o.flushLosses(); err != nil {
		return 0, err
	}
	return cp.Through, nil
}
