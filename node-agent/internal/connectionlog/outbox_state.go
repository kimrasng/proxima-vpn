package connectionlog

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

const (
	MaxOutboxBytes = 256 << 20
	ReservedBytes  = 1 << 20
	stateSlotBytes = ReservedBytes / 2
	maxProducers   = 64
)

var ErrOutboxFull = errors.New("history outbox full")

type checkpoint struct {
	Hello     nodeprov.HistoryHello
	Through   nodeprov.HistoryU64
	Time      *nodeprov.HistoryTime
	Epoch     *nodeprov.HistoryUUID
	Applied   *nodeprov.HistoryApplied
	AppliedAt *nodeprov.HistoryTime
	GapCode   string
	Interval  *collectionInterval
}

type lossState struct {
	Gap      nodeprov.HistoryGap
	Epoch    nodeprov.HistoryUUID
	Revision nodeprov.HistoryU64
}

type diskState struct {
	Generation uint64
	Next       uint64
	AgentBoot  nodeprov.HistoryUUID
	AgentSeq   nodeprov.HistoryU64
	Dropped    nodeprov.HistoryU64
	Sources    map[string]checkpoint
	Losses     map[string]lossState
	Receipt    string
	Active     bool
}

type outbox struct {
	mu      sync.Mutex
	dir     string
	state   *os.File
	disk    diskState
	bytes   int64
	limit   int64
	clock   func() time.Time
	sync    func(*os.File) error
	unclean bool
}

func openOutbox(dir string, clock func() time.Time) (*outbox, error) {
	if err := os.MkdirAll(filepath.Join(dir, "records"), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "state"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	o := &outbox{dir: dir, state: f, limit: MaxOutboxBytes, clock: clock, sync: (*os.File).Sync}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("history directory already owned: %w", err)
	}
	if err := o.loadState(); err != nil {
		f.Close()
		return nil, err
	}
	if err := errors.Join(o.syncDir(o.dir), o.syncDir(filepath.Dir(o.dir))); err != nil {
		f.Close()
		return nil, err
	}
	if err := o.recoverRecords(); err != nil {
		f.Close()
		return nil, err
	}
	o.unclean = o.disk.Active
	if o.unclean {
		for id, cp := range o.disk.Sources {
			if err := o.noteLoss(id, cp, "producer_lost"); err != nil {
				f.Close()
				return nil, err
			}
		}
	}
	o.disk.Active = true
	if err := o.saveState(); err != nil {
		f.Close()
		return nil, err
	}
	return o, nil
}

func (o *outbox) loadState() error {
	info, err := o.state.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		boot, err := nodeprov.NewHistoryUUID()
		if err != nil {
			return err
		}
		o.disk = diskState{AgentBoot: boot, Sources: map[string]checkpoint{}, Losses: map[string]lossState{}}
		// Write every byte, not a sparse truncate: this is reserved loss capacity.
		if _, err := o.state.WriteAt(make([]byte, ReservedBytes), 0); err != nil {
			return err
		}
		if err := o.saveState(); err != nil {
			return err
		}
		return o.syncDir(o.dir)
	}
	var found bool
	for slot := 0; slot < 2; slot++ {
		b := make([]byte, stateSlotBytes)
		if _, err := o.state.ReadAt(b, int64(slot*stateSlotBytes)); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		n := int(binary.BigEndian.Uint32(b[:4]))
		if n == 0 || n > stateSlotBytes-36 {
			continue
		}
		sum := sha256.Sum256(b[36 : 36+n])
		if !bytes.Equal(sum[:], b[4:36]) {
			continue
		}
		var s diskState
		if json.Unmarshal(b[36:36+n], &s) == nil && (!found || s.Generation > o.disk.Generation) {
			o.disk, found = s, true
		}
	}
	if !found || o.disk.Sources == nil || o.disk.Losses == nil || o.disk.AgentBoot.Validate() != nil {
		return fmt.Errorf("history checkpoint corrupt; refusing unproven ACK")
	}
	return nil
}

func (o *outbox) saveState() error {
	o.disk.Generation++
	b, err := json.Marshal(o.disk)
	if err != nil {
		return err
	}
	if len(b) > stateSlotBytes-36 {
		return fmt.Errorf("history reserved checkpoint capacity exhausted")
	}
	frame := make([]byte, 36+len(b))
	binary.BigEndian.PutUint32(frame, uint32(len(b)))
	sum := sha256.Sum256(b)
	copy(frame[4:36], sum[:])
	copy(frame[36:], b)
	if _, err := o.state.WriteAt(frame, int64(o.disk.Generation%2)*stateSlotBytes); err != nil {
		return err
	}
	return o.sync(o.state)
}

func (o *outbox) syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return o.sync(f)
}

func (o *outbox) publish(path string, data []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { f.Close(); os.Remove(path + ".tmp") }()
	if n, err := f.Write(data); err != nil {
		return err
	} else if n != len(data) {
		return io.ErrShortWrite
	}
	if err := o.sync(f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	return o.syncDir(filepath.Dir(path))
}

func (o *outbox) close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.disk.Active = false
	return errors.Join(o.saveState(), o.state.Close())
}
