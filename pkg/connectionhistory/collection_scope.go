package connectionhistory

import (
	"errors"
	"sort"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

var ErrScopeFlow = errors.New("history flow does not belong to collection scope")

// CollectionScope belongs to one live runtime association whose destination
// map exists only for history. Unlike ordinary runtime flow owners, its flows
// are retired on disable and rediscovered as partial observations on enable.
type CollectionScope struct {
	observer *Observer
	flows    map[uint64]*Flow
	reset    func()
	partial  bool
	closed   bool
}

// NewCollectionScope registers an association, not a destination. reset clears
// the runtime's history-only map synchronously on disable and Close. It may be
// nil. The reset callback runs under the observer gate: no I/O, waiting,
// goroutines, or calls to ordinary lock-taking observer/owner/scope methods.
func (o *Observer) NewCollectionScope(reset func()) *CollectionScope {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := &CollectionScope{
		observer: o, flows: make(map[uint64]*Flow), reset: reset,
		partial: !o.enabled,
	}
	o.scopes[s] = struct{}{}
	return s
}

// CollectionGate is valid only inside its synchronous WithEnabled callback.
// Do not retain it or use it from another goroutine. Its operations reuse the
// already-held observer mutex; ordinary Flow methods would deadlock here.
type CollectionGate struct {
	scope *CollectionScope
}

// PartialStart reports whether this association predates the observed epoch.
// Like the other gate operations, it is valid only inside WithEnabled.
func (g *CollectionGate) PartialStart() bool { return g.scope.partial }

// WithEnabled invokes fn only while collection is ON, under the Apply gate.
// OFF returns nil without invoking fn, so destination lookup/allocation must
// occur inside fn. All accesses to the associated history map belong in fn or
// reset; forwarding I/O and other runtime locks must remain outside.
func (s *CollectionScope) WithEnabled(fn func(*CollectionGate) error) error {
	o := s.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !o.enabled {
		return nil
	}
	return fn(&CollectionGate{scope: s})
}

func (g *CollectionGate) NewFlow(info FlowInfo) (*Flow, error) {
	s := g.scope
	info.PartialStart = info.PartialStart || s.partial
	f, err := s.observer.newFlowLocked(info, nil)
	if err != nil {
		return nil, err
	}
	f.collection = s
	s.flows[f.counter] = f
	return f, nil
}

func (g *CollectionGate) Observe(f *Flow, upload, download uint64) error {
	if f.collection != g.scope {
		return ErrScopeFlow
	}
	return f.observeLocked(upload, download)
}

func (g *CollectionGate) SetDestination(f *Flow, effectiveHost *string, observed *nodeprov.HistoryPeer) error {
	if f.collection != g.scope {
		return ErrScopeFlow
	}
	return f.setDestinationLocked(effectiveHost, observed)
}

func (g *CollectionGate) CloseFlow(f *Flow, end End) error {
	if f.collection != g.scope {
		return ErrScopeFlow
	}
	return f.closeLocked(end)
}

// Reparent replaces a scoped observation segment without reacquiring the gate.
// Replace the runtime map entry with the returned flow in the same callback.
func (g *CollectionGate) Reparent(f *Flow, tunnel *Tunnel) (*Flow, error) {
	if f.collection != g.scope {
		return nil, ErrScopeFlow
	}
	return f.reparentLocked(tunnel)
}

// Close follows the real association's quiescence. It finalizes live scoped
// flows while ON, then unregisters and resets even while OFF or if export
// fails. Any finalization error is returned; no end is fabricated while OFF.
// It does not close the association's tunnel or perform runtime I/O.
func (s *CollectionScope) Close(end End) error {
	o := s.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	var result error
	if o.enabled {
		keys := make([]uint64, 0, len(s.flows))
		for k := range s.flows {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			result = errors.Join(result, s.flows[k].closeLocked(end))
		}
	}
	s.closed = true
	delete(o.scopes, s)
	s.resetLocked()
	s.reset = nil
	return result
}

func (s *CollectionScope) resetLocked() {
	for _, f := range s.flows {
		f.retireLocked()
	}
	s.flows = make(map[uint64]*Flow)
	if s.reset != nil {
		s.reset()
	}
}
