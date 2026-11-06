package connectionlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type pendingBatch struct {
	Files []string        `json:"files"`
	Body  json.RawMessage `json:"body"`
}

func (o *outbox) readBatch() (pendingBatch, client.ConnectionHistoryBatch, error) {
	var p pendingBatch
	var b client.ConnectionHistoryBatch
	data, err := os.ReadFile(filepath.Join(o.dir, "pending.json"))
	if err != nil {
		return p, b, err
	}
	f, err := os.Open(filepath.Join(o.dir, "pending.json"))
	if err != nil {
		return p, b, err
	}
	if err := errors.Join(o.sync(f), f.Close(), o.syncDir(o.dir)); err != nil {
		return p, b, err
	}
	if len(data) > 2*client.ConnectionHistoryMaxBatch {
		return p, b, fmt.Errorf("corrupt pending history batch size")
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, b, err
	}
	if err := json.Unmarshal(p.Body, &b); err != nil {
		return p, b, err
	}
	if b.SchemaVersion != 1 || b.BatchID.Validate() != nil || len(b.Events) == 0 ||
		len(b.Events) > 1000 || len(b.Events) != len(p.Files) || len(p.Body) > client.ConnectionHistoryMaxBatch {
		return p, b, fmt.Errorf("corrupt pending history batch")
	}
	for _, name := range p.Files {
		if filepath.Base(name) != name || filepath.Ext(name) != ".jsonl" {
			return p, b, fmt.Errorf("invalid pending history segment")
		}
	}
	digest, err := nodeprov.HistoryEventsDigest(b.Events)
	if err != nil || digest != b.PayloadSHA256 {
		return p, b, fmt.Errorf("history batch digest mismatch: %w", err)
	}
	return p, b, nil
}

func (o *outbox) recoverBatch() error {
	if err := os.Remove(filepath.Join(o.dir, "pending.json.tmp")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	p, b, err := o.readBatch()
	if errors.Is(err, os.ErrNotExist) {
		// A receipt may survive removal of pending.json.
		o.disk.Receipt = ""
		return o.saveState()
	}
	if err != nil {
		return err
	}
	if o.disk.Receipt == string(b.BatchID) {
		if err := o.saveState(); err != nil {
			return err
		}
		return o.reclaim(p)
	}
	return nil
}

func (o *outbox) reclaim(p pendingBatch) error {
	for _, name := range p.Files {
		path := filepath.Join(o.dir, "records", name)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		o.bytes -= recordCharge(info.Size())
	}
	if err := o.syncDir(filepath.Join(o.dir, "records")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(o.dir, "pending.json")); err != nil {
		return err
	}
	if err := o.syncDir(o.dir); err != nil {
		return err
	}
	o.disk.Receipt = ""
	return o.saveState()
}

func (o *outbox) batch() (pendingBatch, client.ConnectionHistoryBatch, error) {
	if err := o.recoverBatch(); err != nil {
		return pendingBatch{}, client.ConnectionHistoryBatch{}, err
	}
	p, b, err := o.readBatch()
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return p, b, err
	}
	entries, err := os.ReadDir(filepath.Join(o.dir, "records"))
	if err != nil {
		return p, b, err
	}
	b = client.ConnectionHistoryBatch{SchemaVersion: 1}
	size := 256
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		ordinal, err := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".jsonl"), 10, 64)
		if err != nil {
			return p, b, err
		}
		if ordinal >= o.disk.Next {
			// Rename may have succeeded before directory fsync failed. The
			// segment is not checkpointed or accounted yet; never upload/reclaim it.
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.dir, "records", entry.Name()))
		if err != nil {
			return p, b, err
		}
		e, err := nodeprov.DecodeHistoryEvent(data)
		if err != nil {
			return p, b, err
		}
		encoded, err := json.Marshal(e)
		if err != nil {
			return p, b, err
		}
		if len(b.Events) == 1000 || size+len(encoded)+1 > client.ConnectionHistoryMaxBatch {
			break
		}
		size += len(encoded) + 1
		b.Events = append(b.Events, e)
		p.Files = append(p.Files, entry.Name())
	}
	if len(b.Events) == 0 {
		if len(o.disk.Losses) > 0 {
			if err := o.flushLosses(); err != nil {
				return p, b, err
			}
			return o.batch()
		}
		return p, b, nil
	}
	b.BatchID, err = nodeprov.NewHistoryUUID()
	if err != nil {
		return p, b, err
	}
	b.PayloadSHA256, err = nodeprov.HistoryEventsDigest(b.Events)
	if err != nil {
		return p, b, err
	}
	p.Body, err = json.Marshal(b)
	if err != nil {
		return p, b, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return p, b, err
	}
	err = o.publish(filepath.Join(o.dir, "pending.json"), data)
	return p, b, err
}

// UploadOnce retries a disk-frozen batch, irrespective of the desired epoch.
func (m *Manager) UploadOnce(ctx context.Context) (result error) {
	m.uploadMu.Lock()
	defer m.uploadMu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.uploadErr = ""
		if result != nil {
			m.uploadErr = result.Error()
		}
		m.notifyLocked()
	}()
	m.out.mu.Lock()
	p, b, err := m.out.batch()
	m.out.mu.Unlock()
	if err != nil || len(b.Events) == 0 {
		return err
	}
	ack, err := m.api.UploadConnectionHistory(ctx, append([]byte(nil), p.Body...))
	if err != nil {
		return err
	}
	if err := ack.Verify(b); err != nil {
		return err
	}
	m.out.mu.Lock()
	defer m.out.mu.Unlock()
	m.out.disk.Receipt = string(b.BatchID)
	if err := m.out.saveState(); err != nil {
		return err
	}
	if err := m.out.reclaim(p); err != nil {
		return err
	}
	return m.out.flushLosses()
}
