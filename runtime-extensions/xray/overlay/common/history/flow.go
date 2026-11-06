package history

import (
	"context"
	"sync"

	ch "github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
)

type Association struct {
	mu              sync.Mutex
	owner           *Owner
	target          net.Destination
	flows           map[string]*ch.Flow
	scope           *ch.CollectionScope
	protocol        string
	inboundTag      string
	active          int
	tasks           int
	upDone          bool
	downDone        bool
	routed          bool
	closed          bool
	retained        bool
	domainRequested bool
	requestCount    int
	endReason       string
	effectiveHost   *string
}

// WrapDispatch instruments the inbound-facing boundary, preserving Stats.
func WrapDispatch(ctx context.Context, target net.Destination, link *transport.Link) (context.Context, *Association) {
	a := newAssociation(ctx, target)
	if a == nil {
		return ctx, nil
	}
	link.Reader = &Reader{Reader: link.Reader, a: a, upload: false}
	link.Writer = &Writer{Writer: link.Writer, a: a, upload: true}
	return context.WithValue(ctx, associationKey{}, a), a
}

// WrapLink instruments supplied upload reader and download writer before sniffing.
func WrapLink(ctx context.Context, target net.Destination, link *transport.Link) (context.Context, *Association) {
	a := newAssociation(ctx, target)
	if a == nil {
		return ctx, nil
	}
	link.Reader = &Reader{Reader: link.Reader, a: a, upload: true}
	link.Writer = &Writer{Writer: link.Writer, a: a, upload: false}
	return context.WithValue(ctx, associationKey{}, a), a
}

func newAssociation(ctx context.Context, target net.Destination) *Association {
	o := FromContext(ctx)
	if o == nil || observer == nil || target.Address.String() == "v1.mux.cool" {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.tunnel == nil {
		return nil
	}
	a := &Association{owner: o, target: target, flows: make(map[string]*ch.Flow)}
	a.protocol = o.protocol
	if in := session.InboundFromContext(ctx); in != nil {
		a.inboundTag = in.Tag
	}
	o.children++
	if target.Network == net.Network_TCP {
		_, err := a.flow(target, false, nil)
		report(err)
	} else {
		a.scope = observer.NewCollectionScope(func() {
			a.flows = make(map[string]*ch.Flow)
			a.domainRequested = false
			a.requestCount = 0
		})
	}
	return a
}

func (a *Association) flow(dest net.Destination, response bool, gate *ch.CollectionGate) (*ch.Flow, error) {
	key := dest.String()
	if response {
		key = "response:" + key
	}
	if f := a.flows[key]; f != nil {
		return f, nil
	}
	host, port := dest.Address.String(), int(dest.Port)
	d := nodeprov.HistoryDestination{RequestedHost: &host, RequestedPort: &port, Transport: "tcp"}
	quality := "exact"
	if dest.Network == net.Network_UDP {
		d.Transport = "udp"
	}
	if response {
		d.RequestedHost, d.RequestedPort = nil, nil
		quality = "observed"
		if dest.Address.Family().IsIP() {
			d.ObservedIP, d.ObservedPort = &host, &port
		} else {
			quality = "unknown"
		}
	}
	info := ch.FlowInfo{Protocol: a.protocol, InboundTag: a.inboundTag, Destination: d, DestinationQuality: quality}
	if a.owner != nil {
		info.Tunnel = a.owner.tunnel
	}
	var f *ch.Flow
	var err error
	if gate != nil {
		f, err = gate.NewFlow(info)
	} else {
		f, err = observer.NewFlow(info)
	}
	if f != nil {
		a.flows[key] = f
		if !response {
			a.requestCount++
			a.domainRequested = a.domainRequested || dest.Address.Family().IsDomain()
		}
	}
	return f, err
}

func (a *Association) observe(mb buf.MultiBuffer, upload bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scope != nil {
		report(a.scope.WithEnabled(func(g *ch.CollectionGate) error {
			return a.observeLocked(mb, upload, g)
		}))
		return
	}
	report(a.observeLocked(mb, upload, nil))
}

func (a *Association) observeLocked(mb buf.MultiBuffer, upload bool, gate *ch.CollectionGate) error {
	for _, b := range mb {
		if b == nil {
			continue
		}
		dest := a.target
		if dest.Network == net.Network_UDP && b.UDP != nil {
			dest = *b.UDP
		}
		_, known := a.flows[dest.String()]
		// A full-cone response cannot distinguish concurrent original domains
		// (or a domain and IP request) that may resolve to the same endpoint.
		ambiguous := a.domainRequested && a.requestCount > 1
		if gate != nil && gate.PartialStart() {
			ambiguous = true
		}
		f, err := a.flow(dest, !upload && (!known || ambiguous) && dest.Network == net.Network_UDP, gate)
		if err != nil {
			return err
		}
		if f == nil {
			continue
		}
		n := uint64(b.Len())
		up, down := uint64(0), uint64(0)
		if upload {
			up = n
		} else {
			down = n
		}
		if gate != nil {
			if err := gate.Observe(f, up, down); err != nil {
				return err
			}
		} else if err := f.Observe(up, down); err != nil {
			return err
		}
	}
	return nil
}

func (a *Association) enter() {
	a.mu.Lock()
	a.active++
	a.mu.Unlock()
}

func (a *Association) leave(upload, done bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active--
	if done {
		if upload {
			a.upDone = true
		} else {
			a.downDone = true
		}
	}
	a.finishLocked()
}

func (a *Association) Routed() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.routed = true
	a.finishLocked()
}

func (a *Association) finishLocked() {
	if a.closed || !a.routed || !a.upDone || !a.downDone || a.active != 0 || a.tasks != 0 {
		return
	}
	a.closed = true
	if a.endReason == "" {
		a.endReason = "transport_close"
	}
	if a.scope != nil {
		report(a.scope.Close(ch.End{Reason: a.endReason, Semantics: "local_owner_closed"}))
	} else {
		for _, f := range a.flows {
			report(f.Close(ch.End{Reason: a.endReason, Semantics: "local_owner_closed"}))
		}
		a.flows = nil
	}
	if a.owner != nil {
		a.owner.mu.Lock()
		defer a.owner.mu.Unlock()
		a.owner.children--
		a.owner.finishLocked()
	}
}
