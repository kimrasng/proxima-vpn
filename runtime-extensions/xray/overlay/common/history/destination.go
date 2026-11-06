package history

import (
	"context"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/xtls/xray-core/common/net"
)

func Effective(ctx context.Context, destination net.Destination) {
	a, _ := ctx.Value(associationKey{}).(*Association)
	if a == nil || a.target.Network != net.Network_TCP {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if destination.Address.String() != a.target.Address.String() {
		host := destination.Address.String()
		a.effectiveHost = &host
	}
	for _, f := range a.flows {
		report(f.SetDestination(a.effectiveHost, nil))
	}
}

// DirectPeer is called only by a final direct outbound. A local SOCKS listener
// is never presented as the remote destination.
func DirectPeer(ctx context.Context, destination net.Destination) {
	a, _ := ctx.Value(associationKey{}).(*Association)
	if a == nil || a.target.Network != net.Network_TCP || !destination.Address.Family().IsIP() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	peer := &nodeprov.HistoryPeer{IP: destination.Address.IP().String(), Port: int(destination.Port)}
	for _, f := range a.flows {
		report(f.SetDestination(a.effectiveHost, peer))
	}
}
