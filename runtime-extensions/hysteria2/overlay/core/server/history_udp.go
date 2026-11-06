package server

import (
	"net/netip"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
)

type udpHistory struct {
	scope            *connectionhistory.CollectionScope
	tunnel           *connectionhistory.Tunnel
	requests         map[string]*connectionhistory.Flow
	effectiveTargets map[string]string
	responses        map[string]*connectionhistory.Flow
}

func newUDPHistory(o *connectionhistory.Observer, t *connectionhistory.Tunnel) *udpHistory {
	u := &udpHistory{tunnel: t}
	u.scope = o.NewCollectionScope(u.reset)
	return u
}

// All map access belongs to WithEnabled or reset under the shared Apply gate.
// No callback does I/O, takes runtime locks, or calls ordinary Flow methods.
// Apply retires the scoped owners and clears these maps before acknowledging OFF.
func (u *udpHistory) reset() {
	u.requests, u.responses, u.effectiveTargets = nil, nil, nil
}

func (u *udpHistory) newFlow(g *connectionhistory.CollectionGate, addr string, response bool) (*connectionhistory.Flow, error) {
	info, err := historyFlowInfo(u.tunnel, addr, "udp", response)
	if err != nil {
		return nil, err
	}
	return g.NewFlow(info)
}

// Feed is called after defragmentation. Held hook packets are observed here
// exactly once; neither hook replay nor outbound writes count them again.
func (u *udpHistory) upload(addr string, n int) {
	err := u.scope.WithEnabled(func(g *connectionhistory.CollectionGate) error {
		f := u.requests[addr]
		if f == nil {
			var err error
			f, err = u.newFlow(g, addr, false)
			if err != nil {
				return err
			}
			if u.requests == nil {
				u.requests = make(map[string]*connectionhistory.Flow)
			}
			u.requests[addr] = f
		}
		return g.Observe(f, uint64(n), 0)
	})
	historyError(err)
}

func (u *udpHistory) effective(original, effective string) {
	err := u.scope.WithEnabled(func(g *connectionhistory.CollectionGate) error {
		f := u.requests[original]
		if f == nil {
			return nil
		}
		if err := g.SetDestination(f, historyEffectiveHost(effective), nil); err != nil {
			return err
		}
		if u.effectiveTargets == nil {
			u.effectiveTargets = make(map[string]string)
		}
		u.effectiveTargets[original] = effective
		return nil
	})
	historyError(err)
}

func sameUDPEndpoint(a, b string) bool {
	left, le := netip.ParseAddrPort(a)
	right, re := netip.ParseAddrPort(b)
	return le == nil && re == nil && left.Port() == right.Port() && left.Addr().Unmap() == right.Addr().Unmap()
}

// Full-cone replies are attributed only when their real endpoint identifies
// exactly one original request. DNS aliases or redirected aliases are not
// guessed; ambiguous/unmatched replies get a response-only flow. Once an OFF
// interval has hidden requests, this association can no longer prove a join.
func (u *udpHistory) download(addr string, n int) {
	err := u.scope.WithEnabled(func(g *connectionhistory.CollectionGate) error {
		var matched *connectionhistory.Flow
		var effectiveHost *string
		count := 0
		unresolved := g.PartialStart()
		for requested, f := range u.requests {
			target := requested
			if effective, ok := u.effectiveTargets[requested]; ok {
				target = effective
			}
			if _, err := netip.ParseAddrPort(target); err != nil {
				unresolved = true
			}
			if sameUDPEndpoint(target, addr) {
				matched = f
				if target != requested {
					effectiveHost = historyEffectiveHost(target)
				}
				count++
			}
		}
		if count != 1 || unresolved {
			matched = u.responses[addr]
			if matched == nil {
				var err error
				matched, err = u.newFlow(g, addr, true)
				if err != nil {
					return err
				}
				if u.responses == nil {
					u.responses = make(map[string]*connectionhistory.Flow)
				}
				u.responses[addr] = matched
			}
		} else if err := g.SetDestination(matched, effectiveHost, historyPeer(addr)); err != nil {
			return err
		}
		return g.Observe(matched, 0, uint64(n))
	})
	historyError(err)
}

// The entry invokes close only after Feed/write and receiveLoop have exited.
func (u *udpHistory) close(reason string) {
	end := connectionhistory.End{Reason: reason, Semantics: "observation_segment_closed"}
	if reason == "idle_timeout" {
		end.Semantics = "activity_expired"
	}
	historyError(u.scope.Close(end))
}
