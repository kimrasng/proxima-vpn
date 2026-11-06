package connectionlog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
)

func TestUploader_ExcludesRenamedSegmentWhoseDirectorySyncFailed(t *testing.T) {
	// Given a real segment renamed on disk but never durably checkpointed.
	wire, api := newWireAPI(t)
	m := newManager(t, api, t.TempDir())
	defer func() { must(t, m.Close()) }()
	h, event := fixture(t)
	h.CollectionEpoch = wire.cfg.CollectionEpoch
	event.CollectionEpoch = *wire.cfg.CollectionEpoch
	_, err := m.out.hello(h)
	must(t, err)
	m.out.sync = func(f *os.File) error {
		if filepath.Base(f.Name()) == "records" {
			return syscall.ENOSPC
		}
		return f.Sync()
	}
	err = m.out.accept(event)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("expected directory durability failure: %v", err)
	}
	if m.out.disk.Next != 0 || m.out.bytes != 0 || m.out.disk.Sources["xray"].Through != 0 {
		t.Fatal("failed publication entered committed membership")
	}
	if _, err := os.Stat(filepath.Join(m.out.dir, "records", "00000000000000000000.jsonl")); err != nil {
		t.Fatal("fixture did not exercise successful rename")
	}
	m.out.sync = (*os.File).Sync
	// When upload runs before the runtime retries the unacknowledged event.
	must(t, m.UploadOnce(context.Background()))
	// Then only its reserved loss is uploaded, never an unaccounted detail record.
	wire.mu.Lock()
	var batch client.ConnectionHistoryBatch
	must(t, json.Unmarshal(wire.bodies[0], &batch))
	wire.mu.Unlock()
	if len(batch.Events) != 1 || batch.Events[0].Kind != "collection_gap" ||
		m.out.bytes != 0 || m.out.disk.Sources["xray"].Through != 0 {
		t.Fatalf("unsafe uncommitted upload/reclaim: %+v bytes=%d", batch, m.out.bytes)
	}
	must(t, m.out.accept(event))
	must(t, m.UploadOnce(context.Background()))
	if m.out.bytes != 0 || m.out.disk.Sources["xray"].Through != 1 {
		t.Fatal("runtime replay did not recover the detailed event")
	}
}
