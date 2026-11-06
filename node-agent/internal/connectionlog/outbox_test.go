package connectionlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func fixture(t *testing.T) (nodeprov.HistoryHello, nodeprov.HistoryEvent) {
	t.Helper()
	boot, epoch := uuid(t), uuid(t)
	h := nodeprov.HistoryHello{Op: "hello", SchemaVersion: 1, ProducerID: "xray", ObserverBootID: boot,
		RuntimeBuild: "test", Capabilities: []string{"hot_toggle"}, FirstAvailableSequence: 1,
		Enabled: true, ConfigRevision: 1, CollectionEpoch: &epoch}
	e := nodeprov.HistoryEvent{SchemaVersion: 1, ObserverBootID: boot, Sequence: 1,
		EventID: string(boot) + ":1", ProducerID: "xray", Runtime: "xray", RuntimeBuild: "test",
		ConfigRevision: 1, CollectionEpoch: epoch, EventTime: nodeprov.HistoryTimestamp(testNow),
		Kind: "collection_boundary", Auth: nodeprov.HistoryAuth{Kind: "unknown"}, Bytes: nodeprov.HistoryBytes{Layer: "none"},
		Lifecycle: nodeprov.HistoryLifecycle{Scope: "collection"},
		Quality: nodeprov.HistoryQuality{Identity: "unknown", Parent: "unknown", Destination: "unknown",
			Bytes: "unknown", Start: "unknown", End: "unknown", Completeness: "complete"},
		Boundary: &nodeprov.HistoryBoundary{Action: "enabled"}}
	must(t, e.Validate())
	return h, e
}

func openTestOutbox(t *testing.T) *outbox {
	t.Helper()
	o, err := openOutbox(t.TempDir(), func() time.Time { return testNow })
	must(t, err)
	t.Cleanup(func() { must(t, o.close()) })
	return o
}

func TestOutbox_DurablePublicationPrecedesCheckpoint(t *testing.T) {
	// Given a real disk outbox with observable fsync boundaries.
	o := openTestOutbox(t)
	h, e := fixture(t)
	_, err := o.hello(h)
	must(t, err)
	var order []string
	o.sync = func(f *os.File) error {
		order = append(order, filepath.Base(f.Name()))
		return f.Sync()
	}
	// When a record is accepted.
	must(t, o.accept(e))
	// Then both the record and directory precede the checkpoint's fsync.
	want := []string{"00000000000000000000.jsonl.tmp", "records", "state"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("durability order %v, want %v", order, want)
	}
}

func TestOutbox_ReservesLossStateAndRecoversAfterFull(t *testing.T) {
	// Given no detail capacity but the preallocated loss/checkpoint reserve.
	o := openTestOutbox(t)
	h, e := fixture(t)
	_, err := o.hello(h)
	must(t, err)
	o.limit = ReservedBytes + 2*client.ConnectionHistoryMaxBatch
	// When accepting a detailed record would exceed the independent history bound.
	err = o.accept(e)
	// Then ACK remains withheld and durable coalesced loss survives recovery.
	if !errors.Is(err, ErrOutboxFull) || o.disk.Sources["xray"].Through != 0 {
		t.Fatalf("full outbox advanced cursor: %v %+v", err, o.disk)
	}
	if len(o.disk.Losses) != 1 {
		t.Fatal("no reserved loss interval")
	}
	o.limit = MaxOutboxBytes
	must(t, o.accept(e))
	if len(o.disk.Losses) != 0 || o.disk.Sources["xray"].Through != 1 {
		t.Fatal("recovery did not durably gap before resuming")
	}
	entries, err := os.ReadDir(filepath.Join(o.dir, "records"))
	must(t, err)
	data, err := os.ReadFile(filepath.Join(o.dir, "records", entries[0].Name()))
	must(t, err)
	gap, err := nodeprov.DecodeHistoryEvent(data)
	must(t, err)
	if gap.Kind != "collection_gap" || gap.Gap.Reason != "outbox_full" {
		t.Fatalf("loss did not precede detail: %+v", gap)
	}
}

func TestOutbox_RejectsUnexplainedSequenceButAcceptsExplicitOverflow(t *testing.T) {
	// Given a producer whose events 1..3 were lost before collection.
	o := openTestOutbox(t)
	h, e := fixture(t)
	_, err := o.hello(h)
	must(t, err)
	e.Sequence, e.EventID = 4, string(e.ObserverBootID)+":4"
	if err := o.accept(e); err == nil {
		t.Fatal("unexplained hole accepted")
	}
	first, last, count := nodeprov.HistoryU64(1), nodeprov.HistoryU64(3), nodeprov.HistoryU64(3)
	e.Kind, e.Boundary, e.Quality.Completeness = "collection_gap", nil, "unknown"
	e.Gap = &nodeprov.HistoryGap{ProducerID: e.ProducerID, ObserverBootID: e.ObserverBootID,
		FirstSequence: &first, LastSequence: &last, LostEvents: &count, Reason: "export_overflow"}
	// When a valid explicit range covers every skipped sequence.
	must(t, o.accept(e))
	// Then the contiguous durable cursor can include that gap.
	if o.disk.Sources["xray"].Through != 4 {
		t.Fatal("valid loss coverage did not advance")
	}
}

func TestOutbox_TornLastSegmentProducesGapWithoutInventedEnd(t *testing.T) {
	// Given committed history followed by a simulated crash during the next append.
	dir := t.TempDir()
	o, err := openOutbox(dir, func() time.Time { return testNow })
	must(t, err)
	h, e := fixture(t)
	_, err = o.hello(h)
	must(t, err)
	must(t, o.accept(e))
	must(t, o.close())
	path := filepath.Join(dir, "records", "00000000000000000001.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	must(t, err)
	_, err = f.Write([]byte(`{"schema_version":1,"event_id":`))
	must(t, err)
	must(t, f.Sync())
	must(t, f.Close())
	// When reopening after the crash.
	o, err = openOutbox(dir, func() time.Time { return testNow })
	must(t, err)
	defer func() { must(t, o.close()) }()
	must(t, o.flushLosses())
	// Then the torn tail is discarded, with an explicit gap, never a fake end.
	entries, err := os.ReadDir(filepath.Join(dir, "records"))
	must(t, err)
	if len(entries) != 2 {
		t.Fatalf("unexpected recovered segments %v", entries)
	}
	data, err := os.ReadFile(filepath.Join(dir, "records", entries[1].Name()))
	must(t, err)
	event, err := nodeprov.DecodeHistoryEvent(data)
	must(t, err)
	if event.Kind != "collection_gap" || event.Gap.Reason != "disk_error" ||
		event.Lifecycle.EndedAt != nil || event.Bytes.Final {
		t.Fatalf("dishonest torn-tail recovery: %+v", event)
	}
}

func TestOutbox_NewRuntimeBootReportsOldBootRestartGap(t *testing.T) {
	// Given an old runtime with durable history but no observed owner close.
	o := openTestOutbox(t)
	h, e := fixture(t)
	_, err := o.hello(h)
	must(t, err)
	must(t, o.accept(e))
	old := h.ObserverBootID
	h.ObserverBootID = uuid(t)
	// When a new runtime hello replaces it.
	through, err := o.hello(h)
	must(t, err)
	// Then the new boot starts at zero and old lifetime ends only in a gap.
	if through != 0 {
		t.Fatal("new runtime inherited old sequence")
	}
	data, err := os.ReadFile(filepath.Join(o.dir, "records", "00000000000000000001.jsonl"))
	must(t, err)
	gap, err := nodeprov.DecodeHistoryEvent(data)
	must(t, err)
	if gap.Kind != "collection_gap" || gap.Gap.ObserverBootID != old || gap.Gap.Reason != "observer_restart" {
		t.Fatalf("missing old-boot gap: %+v", gap)
	}
}

func TestOutbox_TornCheckpointRecoversFsyncedPublishedRecord(t *testing.T) {
	// Given a published record whose latest checkpoint slot was torn by a crash.
	dir := t.TempDir()
	o, err := openOutbox(dir, func() time.Time { return testNow })
	must(t, err)
	h, event := fixture(t)
	_, err = o.hello(h)
	must(t, err)
	must(t, o.accept(event))
	slot := int64(o.disk.Generation%2) * stateSlotBytes
	_, err = o.state.WriteAt([]byte{0, 0, 0, 0}, slot)
	must(t, err)
	must(t, o.state.Sync())
	must(t, o.state.Close()) // Deliberately bypass graceful Close, as a crash does.
	// When the prior valid slot and immutable segment are recovered.
	o, err = openOutbox(dir, func() time.Time { return testNow })
	must(t, err)
	defer func() { must(t, o.close()) }()
	// Then the contiguous checkpoint is reconstructed without accepting torn metadata.
	if o.disk.Sources["xray"].Through != 1 {
		t.Fatal("fsynced record disappeared with torn checkpoint")
	}
	if len(o.disk.Losses) == 0 {
		t.Fatal("unclean boot omitted unknown loss interval")
	}
}

func TestUploader_BoundsBatchesAndPersistsReceiptBeforeReclaim(t *testing.T) {
	// Given more than one batch of real disk records.
	ctx := context.Background()
	_, api := newWireAPI(t)
	m := newManager(t, api, t.TempDir())
	defer func() { must(t, m.Close()) }()
	h, e := fixture(t)
	_, err := m.out.hello(h)
	must(t, err)
	for i := 1; i <= 1001; i++ {
		e.Sequence = nodeprov.HistoryU64(i)
		e.EventID = fmt.Sprintf("%s:%d", e.ObserverBootID, i)
		must(t, m.out.accept(e))
	}
	p, b, err := m.out.batch()
	must(t, err)
	if len(b.Events) == 0 || len(b.Events) > 1000 || len(p.Body) > client.ConnectionHistoryMaxBatch {
		t.Fatalf("batch exceeded wire bounds: %d, %d", len(b.Events), len(p.Body))
	}
	// When a committed receipt cannot fsync, no segment may be reclaimed.
	before := m.out.bytes
	m.out.sync = func(f *os.File) error {
		if filepath.Base(f.Name()) == "state" {
			return errors.New("injected receipt fsync failure")
		}
		return f.Sync()
	}
	m.out.disk.Receipt = string(b.BatchID)
	if err := m.out.saveState(); err == nil {
		t.Fatal("injected receipt durability failure ignored")
	}
	if err := m.UploadOnce(ctx); err == nil {
		t.Fatal("volatile receipt allowed reclamation")
	}
	// Then all immutable bytes remain, including after a failed reclamation attempt.
	if m.out.bytes != before {
		t.Fatal("reclaimed without durable receipt")
	}
	m.out.sync = (*os.File).Sync
	var decoded client.ConnectionHistoryBatch
	must(t, json.Unmarshal(p.Body, &decoded))
	if decoded.BatchID != b.BatchID {
		t.Fatal("pending batch identity changed")
	}
}

func TestUploader_DrainsReservedLossWithoutActiveProducer(t *testing.T) {
	// Given reserved loss state for a supplier that is no longer active.
	wire, api := newWireAPI(t)
	m := newManager(t, api, t.TempDir())
	defer func() { must(t, m.Close()) }()
	h, _ := fixture(t)
	h.CollectionEpoch = wire.cfg.CollectionEpoch
	_, err := m.out.hello(h)
	must(t, err)
	m.out.noteLoss("xray", m.out.disk.Sources["xray"], "disk_error")
	must(t, m.out.saveState())
	// When uploading with no registered source and no detailed segments.
	must(t, m.UploadOnce(context.Background()))
	// Then the loss interval is materialized and acknowledged through real HTTP.
	if len(m.out.disk.Losses) != 0 || m.out.bytes != 0 {
		t.Fatal("inactive supplier's reserved loss never drained")
	}
	wire.mu.Lock()
	defer wire.mu.Unlock()
	if len(wire.bodies) != 1 {
		t.Fatalf("expected one gap batch, got %d", len(wire.bodies))
	}
}
