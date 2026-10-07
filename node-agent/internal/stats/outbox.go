package stats

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
)

// The outbox is intentionally bounded. When full, the collector stops resetting
// Xray counters until delivery frees space; it never discards queued reports.
const (
	maxOutboxBytes = 64 << 20
	maxBatchBytes  = 4 << 20
	maxBatches     = 1024
)

type batch struct {
	ID      string               `json:"batch_id"`
	Hash    string               `json:"hash"`
	Traffic []client.TrafficStat `json:"stats"`
}

type outbox struct {
	dir   string
	files []string // chronological; only names are kept in memory
	bytes int64
}

func openOutbox(dir string) (*outbox, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create stats outbox: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	o := &outbox{dir: dir}
	for _, e := range entries {
		// A crash before rename can leave an uncommitted temporary file; it
		// has never been sent. Remove it so repeated crashes cannot exhaust
		// the disk independently of the queued batch limit.
		if strings.HasPrefix(e.Name(), ".tmp-") {
			if e.IsDir() {
				return nil, fmt.Errorf("unexpected stats outbox entry %q", e.Name())
			}
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return nil, err
			}
			continue
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			return nil, fmt.Errorf("unexpected stats outbox entry %q", e.Name())
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if info.Size() > maxBatchBytes {
			return nil, fmt.Errorf("stats outbox batch %q exceeds size limit", e.Name())
		}
		o.files = append(o.files, e.Name())
		o.bytes += info.Size()
	}
	sort.Strings(o.files)
	// Validate all files at startup rather than silently skipping corruption
	// (which would lose already-reset counters).
	for _, name := range o.files {
		if _, err := o.read(name); err != nil {
			return nil, err
		}
	}
	return o, nil
}

func (o *outbox) full() bool {
	return len(o.files) >= maxBatches || o.bytes >= maxOutboxBytes
}

func newBatch(traffic []client.TrafficStat) (batch, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return batch{}, err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	b := batch{ID: fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), Traffic: traffic}
	encoded, err := json.Marshal(traffic)
	if err != nil {
		return batch{}, err
	}
	hash := sha256.Sum256(encoded)
	b.Hash = hex.EncodeToString(hash[:])
	return b, nil
}

func (o *outbox) append(b batch) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if len(data) > maxBatchBytes || len(o.files) >= maxBatches || o.bytes+int64(len(data)) > maxOutboxBytes {
		return fmt.Errorf("stats outbox full (%d batches, %d bytes); refusing to reset more counters", len(o.files), o.bytes)
	}
	name := fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), b.ID)
	// Sync the new file, atomically publish it, then sync the directory before
	// any POST. Replaying after a lost acknowledgement uses exactly this batch.
	f, err := os.CreateTemp(o.dir, ".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(o.dir, name)); err != nil {
		return err
	}
	if err := syncDir(o.dir); err != nil {
		// The rename may have succeeded. Keep this record in the queue and
		// refuse to send until a later directory sync succeeds.
		o.files = append(o.files, name)
		o.bytes += int64(len(data))
		return err
	}
	o.files = append(o.files, name)
	o.bytes += int64(len(data))
	return nil
}

func (o *outbox) read(name string) (batch, error) {
	var b batch
	data, err := os.ReadFile(filepath.Join(o.dir, name))
	if err != nil {
		return b, err
	}
	if int64(len(data)) > maxBatchBytes || json.Unmarshal(data, &b) != nil || b.ID == "" || len(b.Traffic) == 0 {
		return b, fmt.Errorf("invalid stats outbox batch %q", name)
	}
	encoded, err := json.Marshal(b.Traffic)
	if err != nil {
		return b, err
	}
	hash := sha256.Sum256(encoded)
	if b.Hash != hex.EncodeToString(hash[:]) || !strings.HasSuffix(name, "-"+b.ID+".json") {
		return b, fmt.Errorf("stats outbox batch %q failed integrity check", name)
	}
	return b, nil
}

func (o *outbox) removeFirst() error {
	name := o.files[0]
	info, err := os.Stat(filepath.Join(o.dir, name))
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(o.dir, name)); err != nil {
		return err
	}
	o.files = o.files[1:]
	o.bytes -= info.Size()
	return syncDir(o.dir)
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
