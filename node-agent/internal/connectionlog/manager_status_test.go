package connectionlog

import (
	"context"
	"reflect"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestStatus_AppliedEpochTracksMinimumDurableSupplierNotDesired(t *testing.T) {
	oldEpoch, newEpoch := uuid(t), uuid(t)
	initial := nodeprov.HistoryApplied{}
	on := nodeprov.HistoryApplied{ConfigRevision: 1, Enabled: true, CollectionEpoch: &oldEpoch}
	off := nodeprov.HistoryApplied{ConfigRevision: 2, CollectionEpoch: &oldEpoch}
	next := nodeprov.HistoryApplied{ConfigRevision: 3, Enabled: true, CollectionEpoch: &newEpoch}
	for _, tc := range []struct {
		name    string
		desired nodeprov.HistoryApplied
		xray    *nodeprov.HistoryApplied
		hy      *nodeprov.HistoryApplied
		want    nodeprov.HistoryApplied
	}{
		{"pending_on", on, nil, nil, initial},
		{"partial_initial_on", on, &on, nil, initial},
		{"pending_off", off, &on, &on, on},
		{"partial_off", off, &off, &on, on},
		{"mixed_old_and_new_epochs", next, &next, &off, off},
		{"fully_applied_new_epoch", next, &next, &next, next},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given desired policy and independently durable supplier barriers.
			_, api := newWireAPI(t)
			m := newManager(t, api, t.TempDir())
			defer func() { must(t, m.Close()) }()
			must(t, m.Register(NewUnixSource("xray", "/unused")))
			must(t, m.Register(NewUnixSource("hysteria2", "/unused")))
			m.desired = &client.ConnectionHistoryConfig{SchemaVersion: 1, Revision: tc.desired.ConfigRevision,
				Enabled: tc.desired.Enabled, CollectionEpoch: tc.desired.CollectionEpoch}
			m.out.disk.Sources["xray"] = checkpoint{Applied: tc.xray}
			m.out.disk.Sources["hysteria2"] = checkpoint{Applied: tc.hy}
			for _, worker := range m.workers {
				worker.err = ""
			}
			// When constructing the exact server-facing status.
			s := m.Status()
			// Then epoch and enabled belong to the applied revision, never desired.
			if s.AppliedRevision != tc.want.ConfigRevision || s.Enabled != tc.want.Enabled ||
				!reflect.DeepEqual(s.CollectionEpoch, tc.want.CollectionEpoch) {
				t.Fatalf("inconsistent applied tuple: %+v want %+v", s, tc.want)
			}
			if s.DesiredRevision != tc.desired.ConfigRevision {
				t.Fatal("desired revision disappeared")
			}
			if s.CollectionEpoch != nil {
				*s.CollectionEpoch = uuid(t)
				if reflect.DeepEqual(m.Status().CollectionEpoch, s.CollectionEpoch) {
					t.Fatal("status exposed mutable checkpoint epoch")
				}
			}
		})
	}
}

func TestManager_ReopenWithActiveSuppliersRetainsInactiveBootCheckpoint(t *testing.T) {
	// Given two persisted producer checkpoints and queued history.
	_, api := newWireAPI(t)
	dir := t.TempDir()
	m := newManager(t, api, dir)
	h, e := fixture(t)
	_, err := m.out.hello(h)
	must(t, err)
	must(t, m.out.accept(e))
	other := h
	other.ProducerID, other.ObserverBootID = "hysteria2", uuid(t)
	_, err = m.out.hello(other)
	must(t, err)
	must(t, m.Register(NewUnixSource("xray", "/unused")))
	must(t, m.Register(NewUnixSource("hysteria2", "/unused")))
	queue := m.out.bytes
	must(t, m.Close())
	// When integration reopens with only currently active Hysteria registered.
	m = newManager(t, api, dir)
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(NewUnixSource("hysteria2", "/unused")))
	must(t, m.Poll(context.Background()))
	s := m.Status()
	// Then inactive suppliers do not block status, but their cursor and data survive.
	if len(s.Producers) != 1 || s.Producers[0].ProducerID != "hysteria2" ||
		m.out.disk.Sources["xray"].Hello.ObserverBootID != h.ObserverBootID ||
		m.out.disk.Sources["xray"].Through != 1 || m.out.bytes != queue {
		t.Fatalf("required-set reopen lost durable history: %+v", s)
	}
}
