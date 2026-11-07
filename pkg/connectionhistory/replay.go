package connectionhistory

import (
	"encoding/json"
	"math"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type record struct {
	sequence nodeprov.HistoryU64
	data     []byte
	time     nodeprov.HistoryTime
}

// Overflow drops the entire unacknowledged suffix. This intentionally trades
// retention for a single bounded, exact contiguous loss accumulator. The gap is
// always the first retained frame; a later overflow includes its original range.
// No forwarding callback waits for the consumer or disk.
func (o *Observer) enqueueLocked(r record, lossRevision nodeprov.HistoryU64) error {
	if o.queueBytes+len(r.data) <= o.capacity {
		o.frames = append(o.frames, r)
		o.queueBytes += len(r.data)
		return nil
	}
	first, from := r.sequence, r.time
	if len(o.frames) > 0 {
		first, from = o.frames[0].sequence, o.frames[0].time
	}
	if o.loss != nil && *o.loss.FirstSequence < first {
		first, from = *o.loss.FirstSequence, *o.loss.FromTime
	}
	last := r.sequence
	lost := last - first + 1
	o.loss = &nodeprov.HistoryGap{
		ProducerID: o.options.ProducerID, ObserverBootID: o.boot,
		FirstSequence: ptr(first), LastSequence: ptr(last), LostEvents: ptr(lost),
		FromTime: ptr(from), ToTime: ptr(r.time), Reason: "export_overflow",
	}
	clear(o.frames)
	o.frames, o.queueBytes = o.frames[:0], 0
	o.degradeLocked()
	if o.sequence == math.MaxUint64 {
		return ErrCounterOverflow
	}
	o.sequence++
	gap := o.baseLocked("collection_gap")
	gap.ConfigRevision = lossRevision
	gap.Sequence, gap.EventID = o.sequence, string(o.boot)+":"+o.sequence.String()
	gap.Gap, gap.Quality.Completeness = o.loss, "unknown"
	data, err := nodeprov.EncodeHistoryEvent(gap)
	if err != nil {
		return err
	}
	o.frames = append(o.frames, record{sequence: gap.Sequence, data: data, time: gap.EventTime})
	o.queueBytes = len(data)
	return nil
}

func (o *Observer) Hello() nodeprov.HistoryHello {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.helloLocked()
}
func (o *Observer) helloLocked() nodeprov.HistoryHello {
	first := o.sequence + 1
	if o.sequence == math.MaxUint64 {
		first = o.sequence
	}
	if len(o.frames) > 0 {
		first = o.frames[0].sequence
	}
	return nodeprov.HistoryHello{
		Op: "hello", SchemaVersion: 1, ProducerID: o.options.ProducerID, ObserverBootID: o.boot,
		RuntimeBuild: o.options.RuntimeBuild, Capabilities: append([]string{}, o.options.Capabilities...),
		FirstAvailableSequence: first, LastSequence: o.sequence,
		Enabled: o.enabled, ConfigRevision: o.revision, CollectionEpoch: copyPtr(o.epoch),
	}
}

// Resume returns owned copies. A cursor below the durable ACK watermark cannot
// be reconstructed; callers must retain their ACK checkpoint. A retained gap
// covers every unacknowledged evicted sequence and precedes dependent records.
func (o *Observer) Resume(r nodeprov.HistoryResume) ([][]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.Op != "resume" || r.ObserverBootID != o.boot || r.AfterSequence < o.acked || r.AfterSequence > o.sequence {
		return nil, controlError(o.revision, "export_unavailable")
	}
	out := make([][]byte, 0, len(o.frames))
	for _, f := range o.frames {
		if f.sequence > r.AfterSequence {
			out = append(out, append([]byte(nil), f.data...))
		}
	}
	return out, nil
}

// Ack trusts the authenticated local collector's durable-storage assertion.
// The socket additionally refuses ACKs beyond frames delivered on that session.
func (o *Observer) Ack(a nodeprov.HistoryAck) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if a.Op != "ack" || a.ObserverBootID != o.boot || a.ThroughSequence > o.sequence {
		return controlError(o.revision, "export_unavailable")
	}
	if a.ThroughSequence <= o.acked {
		return nil
	}
	// ACK inside an evicted range would assert an unexplained contiguous prefix.
	if o.loss != nil && a.ThroughSequence >= *o.loss.FirstSequence && a.ThroughSequence <= *o.loss.LastSequence {
		return controlError(o.revision, "export_unavailable")
	}
	o.acked = a.ThroughSequence
	n := 0
	for n < len(o.frames) && o.frames[n].sequence <= o.acked {
		o.queueBytes -= len(o.frames[n].data)
		n++
	}
	left := copy(o.frames, o.frames[n:])
	clear(o.frames[left:])
	o.frames = o.frames[:left]
	if o.loss != nil && o.acked > *o.loss.LastSequence {
		o.loss = nil
	}
	return nil
}

func encodeControl(c nodeprov.HistoryControl) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
