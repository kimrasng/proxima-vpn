// Package history adapts runtime owners to the shared history observer.
package history

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"sync"

	ch "github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/xtls/xray-core/common/session"
)

const Build = "xray-a4a3dff5-history-v1"

var observer *ch.Observer

func newObserver() (*ch.Observer, error) {
	return ch.NewObserver(ch.Options{
		ProducerID: "xray", Runtime: "xray", RuntimeBuild: Build,
		Capabilities: []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"},
	})
}

// Capabilities emits the same strict hello envelope as the local socket.
func Capabilities(output io.Writer) error {
	o, err := newObserver()
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(o.Hello())
}

type (
	ownerKey       struct{}
	associationKey struct{}
	sharedKey      struct{}
)

type Owner struct {
	mu       sync.Mutex
	tunnel   *ch.Tunnel
	protocol string
	activity bool
	closed   bool
	children int
	tasks    int
}

// Start is called before any inbound starts accepting, never by a toggle.
func Start(ctx context.Context) (*ch.Server, error) {
	path := os.Getenv("PROXIMA_HISTORY_SOCKET")
	var err error
	observer, err = newObserver()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	return observer.Listen(ctx, path)
}

func report(err error) {
	if err != nil {
		log.Printf("history observation: %v", err)
	}
}

func Attach(ctx context.Context) (context.Context, *Owner) {
	o := &Owner{}
	return context.WithValue(ctx, ownerKey{}, o), o
}

func FromContext(ctx context.Context) *Owner {
	o, _ := ctx.Value(ownerKey{}).(*Owner)
	return o
}

func Capable(ctx context.Context) bool { return FromContext(ctx) != nil }

func SharedContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, sharedKey{}, true)
}

func IsShared(ctx context.Context) bool {
	shared, _ := ctx.Value(sharedKey{}).(bool)
	return shared
}

// Authenticate receives only the configured, successfully authenticated user.
func Authenticate(ctx context.Context, protocol string, shared, activity bool) {
	o := FromContext(ctx)
	in := session.InboundFromContext(ctx)
	if o == nil || observer == nil || in == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.tunnel != nil {
		return
	}
	labelKind, label := "xray_email", ""
	if in.User != nil {
		label = in.User.Email
	}
	if shared {
		labelKind, label = "shared_inbound", in.Tag
	}
	info := ch.TunnelInfo{Protocol: protocol, InboundTag: in.Tag, LabelKind: labelKind, Label: label}
	if activity {
		info.Scope = "activity_window"
	}
	if in.Source.IsValid() && in.Source.Address.Family().IsIP() {
		info.Peer = &nodeprov.HistoryPeer{IP: in.Source.Address.IP().String(), Port: int(in.Source.Port)}
	}
	t, err := observer.NewTunnel(info)
	report(err)
	o.tunnel = t
	o.protocol = protocol
	o.activity = activity
}

// Close follows actual transport close and waits for registered copy callbacks.
// It does not invent ends for a process crash or collection disable.
func (o *Owner) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	o.finishLocked()
}

func (o *Owner) finishLocked() {
	if o.closed && o.children == 0 && o.tasks == 0 && o.tunnel != nil {
		semantics := "local_owner_closed"
		if o.activity {
			semantics = "observation_segment_closed"
		}
		report(o.tunnel.Close(ch.End{Reason: "transport_close", Semantics: semantics}))
		o.tunnel = nil
	}
}
