package connectionlog

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func offRecoveryCheckpoint(t *testing.T, o *outbox) (nodeprov.HistoryHello, nodeprov.HistoryEvent) {
	t.Helper()
	h, e := fixture(t)
	_, err := o.hello(h)
	must(t, err)
	must(t, o.accept(e))
	e.Sequence, e.EventID = 2, string(e.ObserverBootID)+":2"
	e.ConfigRevision = 2
	e.EventTime = nodeprov.HistoryTimestamp(testNow.Add(time.Second))
	e.Boundary = &nodeprov.HistoryBoundary{Action: "disabled", PriorRevision: 1}
	must(t, o.accept(e))
	cp := o.disk.Sources[h.ProducerID]
	cp.Applied = &nodeprov.HistoryApplied{Op: "applied", ConfigRevision: 2,
		CollectionEpoch: h.CollectionEpoch, BarrierSequence: 2}
	cp.Hello.Enabled, cp.Hello.ConfigRevision = false, 2
	o.disk.Sources[h.ProducerID] = cp
	must(t, o.saveState())
	return h, e
}

func TestOFFRecovery_ClosedIntervalDoesNotInventRestartLoss(t *testing.T) {
	// Given a durable OFF barrier, both clean and unclean producer replacement
	// are outside the collected interval, including old checkpoint schemas.
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "legacy"}[legacy], func(t *testing.T) {
			dir := t.TempDir()
			o, err := openOutbox(dir, func() time.Time { return testNow.Add(2 * time.Second) })
			must(t, err)
			h, _ := offRecoveryCheckpoint(t, o)
			if legacy {
				cp := o.disk.Sources[h.ProducerID]
				// Recreate the checkpoint fields present before this correction.
				o.disk.Sources[h.ProducerID] = checkpoint{Hello: cp.Hello, Through: cp.Through,
					Time: cp.Time, Epoch: cp.Epoch, Applied: cp.Applied, AppliedAt: cp.AppliedAt}
				must(t, o.saveState())
			}
			must(t, o.state.Close())
			// When the agent restarts uncleanly and then sees a fresh OFF boot.
			o, err = openOutbox(dir, func() time.Time { return testNow.Add(3 * time.Second) })
			must(t, err)
			defer func() { must(t, o.close()) }()
			h.ObserverBootID, h.Enabled, h.ConfigRevision, h.CollectionEpoch = uuid(t), false, 0, nil
			_, err = o.hello(h)
			must(t, err)
			// Then only the original enabled/disabled boundaries remain queued.
			_, batch, err := o.batch()
			must(t, err)
			if len(batch.Events) != 2 || len(o.disk.Losses) != 0 {
				t.Fatalf("closed OFF interval invented loss: %+v", batch.Events)
			}
		})
	}
}

func TestOFFRecovery_QueuedLossKeepsActiveRevisionAndClosedBound(t *testing.T) {
	// Given old durable records that are still queued after applied OFF.
	o := openTestOutbox(t)
	h, boundary := offRecoveryCheckpoint(t, o)
	// When recovery discovers a real disk loss, OFF must not hide it.
	o.noteLoss(h.ProducerID, o.disk.Sources[h.ProducerID], "disk_error")
	must(t, o.saveState())
	must(t, o.flushLosses())
	// Then the gap belongs to the enabled revision and stops at the OFF barrier.
	_, batch, err := o.batch()
	must(t, err)
	if len(batch.Events) != 3 {
		t.Fatalf("known queued loss disappeared: %+v", batch.Events)
	}
	gap := batch.Events[2]
	if gap.Gap == nil || gap.ConfigRevision != 1 || gap.CollectionEpoch != boundary.CollectionEpoch ||
		gap.Gap.ToTime == nil || *gap.Gap.ToTime != boundary.EventTime {
		t.Fatalf("queued loss escaped active interval: %+v", gap)
	}
}

func TestOFFRecovery_UnacknowledgedOFFDoesNotCloseDurableInterval(t *testing.T) {
	// Given only the enabled boundary is durable, but the runtime hello is OFF.
	o := openTestOutbox(t)
	h, e := fixture(t)
	_, err := o.hello(h)
	must(t, err)
	must(t, o.accept(e))
	h.Enabled, h.ConfigRevision, h.LastSequence = false, 2, 2
	_, err = o.hello(h)
	must(t, err)
	// When that runtime disappears before its disabling boundary is durable.
	h.ObserverBootID, h.ConfigRevision, h.CollectionEpoch, h.LastSequence = uuid(t), 0, nil, 0
	_, err = o.hello(h)
	must(t, err)
	// Then uncertainty remains attributed to the enabled interval, not OFF.
	_, batch, err := o.batch()
	must(t, err)
	if len(batch.Events) != 2 || batch.Events[1].Gap == nil || batch.Events[1].ConfigRevision != 1 {
		t.Fatalf("undrained OFF discarded or misattributed loss: %+v", batch.Events)
	}
}

func TestOFFRecovery_NewProducerAppliesClosedPolicyWithoutCollection(t *testing.T) {
	// Given a fresh observer and a server policy retaining an old closed epoch.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, api := newWireAPI(t)
	wire.cfg.Enabled, wire.cfg.Revision = false, 2
	obs, socket := observer(t, ctx, "xray")
	m := newManager(t, api, t.TempDir())
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(NewUnixSource("xray", socket)))
	must(t, m.Poll(ctx))
	// When the agent applies that policy across the real exporter socket.
	done := make(chan error, 1)
	go func() {
		err := m.collect(ctx, m.workers["xray"])
		m.workerError(m.workers["xray"], err)
		done <- err
	}()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		changed := m.Changes()
		s := m.Status()
		if s.AppliedRevision == 2 || (s.ErrorCode != "" && s.ErrorCode != "export_unavailable") {
			if s.State != "off" || s.CollectionEpoch == nil || *s.CollectionEpoch != *wire.cfg.CollectionEpoch {
				t.Fatalf("fresh observer cannot acknowledge closed OFF policy: %+v", s)
			}
			break
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatal("OFF barrier was not acknowledged")
		}
	}
	// Then no boundary, gap or collection was invented on the new producer.
	if h := obs.Hello(); h.Enabled || h.LastSequence != 0 || h.CollectionEpoch != nil {
		t.Fatalf("initial OFF manufactured an interval: %+v", h)
	}
	entries, err := os.ReadDir(m.out.dir + "/records")
	must(t, err)
	if len(entries) != 0 {
		t.Fatal("initial OFF generated history")
	}
}
