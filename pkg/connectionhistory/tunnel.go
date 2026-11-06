package connectionhistory

import (
	"fmt"
	"math"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// TunnelInfo is supplied only AFTER runtime authentication succeeds. Label is
// the authenticator's configured identity, never a client claim or password.
type TunnelInfo struct {
	Protocol     string
	InboundTag   string
	LabelKind    string
	Label        string
	Peer         *nodeprov.HistoryPeer
	Scope        string
	PartialStart bool
}

type Tunnel struct {
	observer     *Observer
	counter      uint64
	id           string
	info         TunnelInfo
	auth         nodeprov.HistoryAuth
	published    bool
	closed       bool
	children     int
	started      *nodeprov.HistoryTime
	last         nodeprov.HistoryTime
	partial      bool
	completeness string
	lastUpdate   nodeprov.HistoryU64
}

func (o *Observer) NewTunnel(info TunnelInfo) (*Tunnel, error) {
	if info.Scope == "" {
		info.Scope = "authenticated_transport"
	}
	info.Peer = copyPtr(info.Peer)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.tunnelCounter == math.MaxUint64 {
		return nil, ErrCounterOverflow
	}
	t := &Tunnel{observer: o, counter: o.tunnelCounter + 1, info: info, auth: o.resolveLocked(info)}
	t.id = fmt.Sprintf("%s:t:%d", o.boot, t.counter)
	t.beginEpochLocked(false)
	e := t.eventLocked("tunnel_start", nil)
	if err := o.validateOwnerEventLocked(e); err != nil {
		return nil, err
	}
	o.tunnelCounter++
	o.tunnels[t.counter] = t
	if o.enabled {
		if err := t.emitLocked("tunnel_start", nil); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *Tunnel) ID() string { return t.id }

func (t *Tunnel) beginEpochLocked(partial bool) {
	partial = partial || t.info.PartialStart
	t.partial, t.completeness = partial, "complete"
	t.last, t.lastUpdate = t.observer.nowLocked()
	t.started = ptr(t.last)
	if partial {
		t.started, t.completeness = nil, "partial"
	}
	if !t.published && t.auth.Kind == "unknown" {
		t.auth = t.observer.resolveLocked(t.info)
	}
}

func (t *Tunnel) eventLocked(kind string, end *End) nodeprov.HistoryEvent {
	e := t.observer.baseLocked(kind)
	e.TunnelID, e.Protocol, e.InboundTag, e.Peer = ptr(t.id), ptr(t.info.Protocol), ptr(t.info.InboundTag), t.info.Peer
	e.Auth = t.auth
	e.Lifecycle = nodeprov.HistoryLifecycle{
		Scope: t.info.Scope, StartedAt: t.started, LastObservedAt: ptr(t.last), PartialStart: t.partial,
	}
	e.Quality.Completeness = t.completeness
	if t.auth.Kind == "device" {
		e.Quality.Identity = "exact"
	}
	setEndpoints(&e, end)
	return e
}
func (t *Tunnel) emitLocked(kind string, end *End) error {
	t.published = true
	e := t.eventLocked(kind, end)
	t.lastUpdate = e.MonotonicNS
	return t.observer.emitLocked(e)
}

// End describes an actual quiescent local owner, never collection disable or
// process disappearance. Runtime integrations must join their forwarding tasks.
type End struct {
	Reason    string
	Semantics string
}

func setEndpoints(e *nodeprov.HistoryEvent, end *End) {
	if e.Lifecycle.StartedAt != nil {
		e.Quality.Start = "exact"
		if e.Lifecycle.Scope == "activity_window" {
			e.Quality.Start = "observed"
		}
	}
	if end != nil {
		e.Lifecycle.EndedAt = ptr(e.EventTime)
		e.Lifecycle.EndReason, e.Lifecycle.EndSemantics = ptr(end.Reason), ptr(end.Semantics)
		e.Quality.End = "exact"
		if e.Lifecycle.Scope == "activity_window" || end.Semantics != "local_owner_closed" {
			e.Quality.End = "observed"
		}
	}
}

func (t *Tunnel) Close(end End) error {
	o := t.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if t.children != 0 {
		return ErrChildrenLive
	}
	e := t.eventLocked("tunnel_end", &end)
	if err := o.validateOwnerEventLocked(e); err != nil {
		return err
	}
	if o.enabled {
		if err := t.emitLocked("tunnel_end", &end); err != nil {
			return err
		}
	}
	t.closed = true
	delete(o.tunnels, t.counter)
	return nil
}

// Validate even while OFF, using a validation-only epoch not emitted on wire.
func (o *Observer) validateOwnerEventLocked(e nodeprov.HistoryEvent) error {
	e.Sequence = 1
	e.EventID = string(o.boot) + ":1"
	if o.epoch == nil {
		e.CollectionEpoch = o.boot
	}
	return e.Validate()
}
