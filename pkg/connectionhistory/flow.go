package connectionhistory

import (
	"fmt"
	"math"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type FlowInfo struct {
	Tunnel *Tunnel
	// Protocol/inbound/peer are inherited from Tunnel when it is present.
	Protocol           string
	InboundTag         string
	Peer               *nodeprov.HistoryPeer
	Destination        nodeprov.HistoryDestination
	DestinationQuality string
	Scope              string
	Layer              string
	PartialStart       bool
}

type Flow struct {
	observer     *Observer
	collection   *CollectionScope
	counter      uint64
	id           string
	info         FlowInfo
	auth         nodeprov.HistoryAuth
	published    bool
	closed       bool
	upload       nodeprov.HistoryU64
	download     nodeprov.HistoryU64
	overflow     bool
	started      *nodeprov.HistoryTime
	last         nodeprov.HistoryTime
	partial      bool
	completeness string
	lastUpdate   nodeprov.HistoryU64
}

func (o *Observer) NewFlow(info FlowInfo) (*Flow, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.newFlowLocked(info, nil)
}

func (o *Observer) newFlowLocked(info FlowInfo, previous *Flow) (*Flow, error) {
	if info.Scope == "" {
		info.Scope = "proxy_flow"
	}
	if info.Layer == "" {
		info.Layer = "proxy_payload_observed"
	}
	if info.DestinationQuality == "" {
		info.DestinationQuality = "exact"
	}
	d := &info.Destination
	d.RequestedHost, d.EffectiveHost, d.ObservedIP = copyPtr(d.RequestedHost), copyPtr(d.EffectiveHost), copyPtr(d.ObservedIP)
	d.RequestedPort, d.ObservedPort = copyPtr(d.RequestedPort), copyPtr(d.ObservedPort)
	info.Peer = copyPtr(info.Peer)
	auth := nodeprov.HistoryAuth{Kind: "unknown"}
	if t := info.Tunnel; t != nil {
		if t.observer != o || t.closed {
			return nil, ErrClosed
		}
		info.Protocol, info.InboundTag, info.Peer = t.info.Protocol, t.info.InboundTag, t.info.Peer
		auth = t.auth
	}
	if o.flowCounter == math.MaxUint64 {
		return nil, ErrCounterOverflow
	}
	f := &Flow{observer: o, counter: o.flowCounter + 1, info: info, auth: auth}
	f.id = fmt.Sprintf("%s:f:%d", o.boot, f.counter)
	f.beginEpochLocked(false)
	if err := o.validateOwnerEventLocked(f.eventLocked("flow_start", nil)); err != nil {
		return nil, err
	}
	if previous != nil {
		f.collection = previous.collection
		if err := previous.closeLocked(End{Reason: "parent_changed", Semantics: "observation_segment_closed"}); err != nil {
			return nil, err
		}
	}
	o.flowCounter++
	o.flows[f.counter] = f
	if f.collection != nil {
		f.collection.flows[f.counter] = f
	}
	if info.Tunnel != nil {
		info.Tunnel.children++
	}
	if o.enabled {
		if err := f.emitLocked("flow_start", nil); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (f *Flow) ID() string { return f.id }

// Reparent atomically ends this observation segment and opens a fresh one
// under the new authenticated parent, without touching data-plane state.
// The runtime must hold its association handoff lock and quiesce in-flight
// callbacks BEFORE calling, then publish the returned handle to new callbacks.
// Apply cannot interleave the old end and new zero-baseline start.
func (f *Flow) Reparent(tunnel *Tunnel) (*Flow, error) {
	o := f.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	return f.reparentLocked(tunnel)
}

func (f *Flow) reparentLocked(tunnel *Tunnel) (*Flow, error) {
	if f.closed {
		return nil, ErrClosed
	}
	info := f.info
	info.Tunnel = tunnel
	return f.observer.newFlowLocked(info, f)
}
func (f *Flow) beginEpochLocked(partial bool) {
	partial = partial || f.info.PartialStart
	f.upload, f.download, f.overflow = 0, 0, false
	f.partial, f.completeness = partial, "complete"
	f.last, f.lastUpdate = f.observer.nowLocked()
	f.started = ptr(f.last)
	if partial {
		f.started, f.completeness = nil, "partial"
	}
	if !f.published && f.info.Tunnel != nil {
		f.auth = f.info.Tunnel.auth
	}
}
func (f *Flow) eventLocked(kind string, end *End) nodeprov.HistoryEvent {
	e := f.observer.baseLocked(kind)
	e.FlowID, e.Protocol, e.InboundTag, e.Peer = ptr(f.id), ptr(f.info.Protocol), ptr(f.info.InboundTag), f.info.Peer
	if f.info.Tunnel != nil {
		e.TunnelID, e.Quality.Parent = ptr(f.info.Tunnel.id), "exact"
	}
	e.Auth, e.Destination = f.auth, &f.info.Destination
	if f.auth.Kind == "device" {
		e.Quality.Identity = "exact"
	}
	e.Quality.Destination, e.Quality.Bytes, e.Quality.Completeness = f.info.DestinationQuality, "exact", f.completeness
	e.Bytes = nodeprov.HistoryBytes{Layer: f.info.Layer, Upload: ptr(f.upload), Download: ptr(f.download), Final: end != nil && f.completeness != "unknown"}
	if f.overflow {
		e.Bytes.Upload, e.Bytes.Download, e.Bytes.Final = nil, nil, false
		e.Quality.Bytes, e.Quality.Completeness = "unknown", "unknown"
	}
	e.Lifecycle = nodeprov.HistoryLifecycle{
		Scope: f.info.Scope, StartedAt: f.started, LastObservedAt: ptr(f.last), PartialStart: f.partial,
	}
	setEndpoints(&e, end)
	return e
}
func (f *Flow) emitLocked(kind string, end *End) error {
	f.published = true
	e := f.eventLocked(kind, end)
	f.lastUpdate = e.MonotonicNS
	return f.observer.emitLocked(e)
}

// Observe counts each payload once at the runtime boundary, including n>0 with
// an I/O error. It does no socket/disk I/O. OFF observations are discarded.
func (f *Flow) Observe(upload, download uint64) error {
	o := f.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	return f.observeLocked(upload, download)
}

func (f *Flow) observeLocked(upload, download uint64) error {
	o := f.observer
	if f.closed {
		return ErrClosed
	}
	if !o.enabled {
		return nil
	}
	f.last, _ = o.nowLocked()
	if f.info.Tunnel != nil {
		f.info.Tunnel.last = f.last
	}
	if f.overflow {
		return ErrCounterOverflow
	}
	if upload > math.MaxUint64-uint64(f.upload) || download > math.MaxUint64-uint64(f.download) {
		f.overflow = true
		o.degradeLocked()
		e := o.baseLocked("collection_gap")
		e.Quality.Completeness = "unknown"
		e.Gap = &nodeprov.HistoryGap{ProducerID: o.options.ProducerID, ObserverBootID: o.boot, FromTime: ptr(f.last), ToTime: ptr(f.last), Reason: "counter_overflow"}
		if err := o.emitLocked(e); err != nil {
			return err
		}
		return ErrCounterOverflow
	}
	f.upload += nodeprov.HistoryU64(upload)
	f.download += nodeprov.HistoryU64(download)
	return nil
}

// SetDestination refines only the effective target and proven real outbound
// endpoint. The original request and parent/binding never change. Nil clears
// an unavailable value. Updates appear in the next cumulative snapshot/end.
func (f *Flow) SetDestination(effectiveHost *string, observed *nodeprov.HistoryPeer) error {
	o := f.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	return f.setDestinationLocked(effectiveHost, observed)
}

func (f *Flow) setDestinationLocked(effectiveHost *string, observed *nodeprov.HistoryPeer) error {
	if f.closed {
		return ErrClosed
	}
	d := f.info.Destination
	d.EffectiveHost = copyPtr(effectiveHost)
	d.ObservedIP, d.ObservedPort = nil, nil
	if observed != nil {
		d.ObservedIP, d.ObservedPort = ptr(observed.IP), ptr(observed.Port)
	}
	if err := d.Validate(); err != nil {
		return err
	}
	f.info.Destination = d
	return nil
}

// Close is called only after BOTH runtime directions (and outbound invocation)
// have finished. Concurrent Observe/Close/Apply linearize on the same gate.
// A late Observe returns ErrClosed; it never silently mutates a terminal sample.
func (f *Flow) Close(end End) error {
	o := f.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	return f.closeLocked(end)
}

func (f *Flow) closeLocked(end End) error {
	o := f.observer
	if f.closed {
		return ErrClosed
	}
	if err := o.validateOwnerEventLocked(f.eventLocked("flow_end", &end)); err != nil {
		return err
	}
	if o.enabled {
		if err := f.emitLocked("flow_end", &end); err != nil {
			return err
		}
	}
	f.retireLocked()
	return nil
}

// Retirement only releases tracking. Disable must not invent a terminal event.
func (f *Flow) retireLocked() {
	o := f.observer
	f.closed = true
	delete(o.flows, f.counter)
	if f.collection != nil {
		delete(f.collection.flows, f.counter)
	}
	if f.info.Tunnel != nil {
		f.info.Tunnel.children--
	}
	// Drop destination metadata as soon as the real owner closes, also OFF.
	f.info.Destination = nodeprov.HistoryDestination{}
}
