package history

import (
	"context"

	ch "github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

// PipeReader preserves XUDP's retained-pipe operations without bypassing
// observation on ordinary reads.
func PipeReader(r buf.Reader) *pipe.Reader {
	if wrapped, ok := r.(*Reader); ok {
		r = wrapped.Reader
	}
	return r.(*pipe.Reader)
}

// Preserve carries observation ownership into upstream's independent timeout
// context. It intentionally does not change that context's cancellation.
func Preserve(from, to context.Context) context.Context {
	if o := FromContext(from); o != nil {
		to = context.WithValue(to, ownerKey{}, o)
	}
	if a, ok := from.Value(associationKey{}).(*Association); ok {
		to = context.WithValue(to, associationKey{}, a)
	}
	return to
}

// Reattach serializes observation updates at the retained association boundary.
// The caller first detaches and joins the previous mux response copy.
func Reattach(ctx context.Context, output buf.Writer) {
	w, ok := output.(*Writer)
	if !ok {
		return
	}
	o := FromContext(ctx)
	if o == nil {
		return
	}
	a := w.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.owner == o {
		return
	}
	a.reparentLocked(o)
	a.downDone = false
}

// Retain marks the association, not a particular mux session. Its network link
// can survive a carrier; its observation segment cannot retain that parent.
func Retain(link *transport.Link) {
	if w, ok := link.Writer.(*Writer); ok {
		w.a.mu.Lock()
		w.a.retained = true
		w.a.mu.Unlock()
	}
}

func (a *Association) reparentLocked(o *Owner) {
	if a.owner == o {
		return
	}
	var tunnel *ch.Tunnel
	if o != nil {
		o.mu.Lock()
		tunnel = o.tunnel
		if tunnel == nil {
			o.mu.Unlock()
			return
		}
		o.children++
		o.mu.Unlock()
	}
	if a.scope != nil {
		report(a.scope.WithEnabled(func(g *ch.CollectionGate) error {
			for key, f := range a.flows {
				next, err := g.Reparent(f, tunnel)
				if err != nil {
					return err
				}
				a.flows[key] = next
			}
			return nil
		}))
	} else {
		for key, f := range a.flows {
			next, err := f.Reparent(tunnel)
			report(err)
			if next != nil {
				a.flows[key] = next
			}
		}
	}
	if a.owner != nil {
		a.owner.mu.Lock()
		a.owner.children--
		a.owner.finishLocked()
		a.owner.mu.Unlock()
	}
	a.owner = o
}

// TrackLink registers a mux response callback before its goroutine starts.
func TrackLink(link *transport.Link) func() {
	w, ok := link.Writer.(*Writer)
	if !ok {
		return func() {}
	}
	a := w.a
	a.mu.Lock()
	a.tasks++
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.tasks--
		// The mux response copy has returned and its caller has joined all
		// accepted session writes. No old-parent callback can enter after this.
		if a.retained {
			a.reparentLocked(nil)
		}
		a.finishLocked()
	}
}
