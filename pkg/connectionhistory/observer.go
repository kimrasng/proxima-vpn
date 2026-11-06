// Package connectionhistory supplies the shared runtime-owned history gate and
// private control exporter. It never performs forwarding, attribution by IP,
// disk persistence, or network I/O while holding the observation gate.
package connectionhistory

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

const ReplayCapacity = 8 * 1024 * 1024
const UpdateInterval = 10 * time.Second

var (
	ErrClosed          = errors.New("history owner closed")
	ErrChildrenLive    = errors.New("history tunnel still has live flows")
	ErrCounterOverflow = errors.New("history counter overflow")
)

type Options struct {
	ProducerID   string
	Runtime      string
	RuntimeBuild string
	Capabilities []string
	// Clock must be concurrency safe. Production leaves it nil for time.Now.
	Clock func() time.Time
}

type Observer struct {
	mu            sync.Mutex
	options       Options
	boot          nodeprov.HistoryUUID
	start         time.Time
	mono          nodeprov.HistoryU64
	sequence      nodeprov.HistoryU64
	tunnelCounter uint64
	flowCounter   uint64
	tunnels       map[uint64]*Tunnel
	flows         map[uint64]*Flow
	enabled       bool
	revision      nodeprov.HistoryU64
	epoch         *nodeprov.HistoryUUID
	usedEpochs    map[nodeprov.HistoryUUID]struct{}
	bindings      map[bindingKey]nodeprov.HistoryAuth
	desired       []byte
	applied       nodeprov.HistoryApplied
	frames        []record
	queueBytes    int
	capacity      int
	acked         nodeprov.HistoryU64
	loss          *nodeprov.HistoryGap
	wake          chan struct{}
}

func NewObserver(options Options) (*Observer, error) {
	boot, err := nodeprov.NewHistoryUUID()
	if err != nil {
		return nil, err
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	options.Capabilities = append([]string{}, options.Capabilities...)
	sort.Strings(options.Capabilities)
	o := &Observer{
		options: options, boot: boot, start: options.Clock(),
		tunnels: make(map[uint64]*Tunnel), flows: make(map[uint64]*Flow),
		usedEpochs: make(map[nodeprov.HistoryUUID]struct{}),
		bindings:   make(map[bindingKey]nodeprov.HistoryAuth),
		capacity:   ReplayCapacity, wake: make(chan struct{}, 1),
	}
	b, err := encodeControl(o.helloLocked())
	if err != nil {
		return nil, err
	}
	if _, err := nodeprov.DecodeHistoryControl(b); err != nil {
		return nil, err
	}
	switch options.Runtime {
	case "xray", "hysteria2", "wireguard", "agent":
	default:
		return nil, fmt.Errorf("%w: runtime", nodeprov.ErrHistoryInvalid)
	}
	return o, nil
}

func (o *Observer) BootID() nodeprov.HistoryUUID { return o.boot }

func ptr[T any](v T) *T { return &v }
func copyPtr[T any](v *T) *T {
	if v == nil {
		return nil
	}
	return ptr(*v)
}

func (o *Observer) nowLocked() (nodeprov.HistoryTime, nodeprov.HistoryU64) {
	now := o.options.Clock()
	d := now.Sub(o.start)
	if d > 0 && uint64(d) > uint64(o.mono) {
		o.mono = nodeprov.HistoryU64(d)
	}
	return nodeprov.HistoryTimestamp(now), o.mono
}

func unknownQuality() nodeprov.HistoryQuality {
	return nodeprov.HistoryQuality{
		Identity: "unknown", Parent: "unknown", Destination: "unknown", Bytes: "unknown",
		Start: "unknown", End: "unknown", Completeness: "complete",
	}
}

func (o *Observer) baseLocked(kind string) nodeprov.HistoryEvent {
	t, mono := o.nowLocked()
	e := nodeprov.HistoryEvent{
		SchemaVersion: 1, ObserverBootID: o.boot, ProducerID: o.options.ProducerID,
		Runtime: o.options.Runtime, RuntimeBuild: o.options.RuntimeBuild,
		ConfigRevision: o.revision, EventTime: t, MonotonicNS: mono, Kind: kind,
		Auth: nodeprov.HistoryAuth{Kind: "unknown"}, Bytes: nodeprov.HistoryBytes{Layer: "none"},
		Lifecycle: nodeprov.HistoryLifecycle{Scope: "collection"}, Quality: unknownQuality(),
	}
	if o.epoch != nil {
		e.CollectionEpoch = *o.epoch
	}
	return e
}

func (o *Observer) emitLocked(e nodeprov.HistoryEvent) error {
	if o.sequence == math.MaxUint64 {
		return ErrCounterOverflow
	}
	o.sequence++
	e.Sequence = o.sequence
	e.EventID = string(o.boot) + ":" + o.sequence.String()
	data, err := nodeprov.EncodeHistoryEvent(e)
	if err != nil {
		return err
	}
	if err := o.enqueueLocked(record{sequence: e.Sequence, data: data, time: e.EventTime}); err != nil {
		return err
	}
	select {
	case o.wake <- struct{}{}:
	default:
	}
	return nil
}

// Snapshot emits due cumulative updates. Disable bypasses this throttle to
// guarantee a final nonterminal sample at its barrier.
func (o *Observer) Snapshot() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.enabled {
		return nil
	}
	_, mono := o.nowLocked()
	for _, t := range o.sortedTunnelsLocked() {
		if mono-t.lastUpdate >= nodeprov.HistoryU64(UpdateInterval) {
			if err := t.emitLocked("tunnel_update", nil); err != nil {
				return err
			}
		}
	}
	for _, f := range o.sortedFlowsLocked() {
		if mono-f.lastUpdate >= nodeprov.HistoryU64(UpdateInterval) {
			if err := f.emitLocked("flow_update", nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReportGap records supplier loss, without inventing entity ends. Retained byte
// samples remain valid, but every live observation loses completeness.
func (o *Observer) ReportGap(gap nodeprov.HistoryGap) error {
	if err := gap.Validate(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.enabled {
		return nil
	}
	o.degradeLocked()
	e := o.baseLocked("collection_gap")
	e.Gap = &gap
	e.Quality.Completeness = "unknown"
	return o.emitLocked(e)
}

func (o *Observer) degradeLocked() {
	for _, t := range o.tunnels {
		t.completeness = "unknown"
	}
	for _, f := range o.flows {
		f.completeness = "unknown"
	}
}

func (o *Observer) sortedTunnelsLocked() []*Tunnel {
	keys := make([]uint64, 0, len(o.tunnels))
	for k := range o.tunnels {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]*Tunnel, 0, len(keys))
	for _, k := range keys {
		out = append(out, o.tunnels[k])
	}
	return out
}
func (o *Observer) sortedFlowsLocked() []*Flow {
	keys := make([]uint64, 0, len(o.flows))
	for k := range o.flows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]*Flow, 0, len(keys))
	for _, k := range keys {
		out = append(out, o.flows[k])
	}
	return out
}
