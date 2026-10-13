package xray

import (
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const maxAccessPeers = 262144

// AccessPeer is an authenticated TCP peer learned from Xray's access log.
// It is not proof of a currently live connection. Consumers must join it with
// live socket/activity observations before counting or enforcing anything.
type AccessPeer struct {
	UUID       string
	Peer       netip.AddrPort
	AcceptedAt time.Time
}

// AccessIndex associates authenticated UUIDs with transport peers. It ignores
// UDP logical destinations and API connections, neither of which identifies
// the outer TCP tunnel. Conflicting credentials on a reused peer make that
// peer ambiguous until the process generation is reset.
type AccessIndex struct {
	mu        sync.Mutex
	peers     map[netip.AddrPort]AccessPeer
	ambiguous map[netip.AddrPort]bool
	overflow  bool
}

func NewAccessIndex() *AccessIndex {
	index := &AccessIndex{}
	index.Reset()
	return index
}

// Reset begins a new Xray process generation. Old peer ownership must never
// leak into a restarted process which can reuse the same TCP tuples.
func (i *AccessIndex) Reset() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.peers = make(map[netip.AddrPort]AccessPeer)
	i.ambiguous = make(map[netip.AddrPort]bool)
	i.overflow = false
}

func (i *AccessIndex) Observe(line string, now time.Time) {
	peer, ok := parseAccessPeer(line, now)
	if !ok {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ambiguous[peer.Peer] {
		return
	}
	if previous, exists := i.peers[peer.Peer]; exists {
		if previous.UUID != peer.UUID {
			delete(i.peers, peer.Peer)
			i.ambiguous[peer.Peer] = true
		}
		return // preserve the first authenticated observation, not destination activity
	}
	if len(i.peers)+len(i.ambiguous) >= maxAccessPeers {
		i.overflow = true
		return
	}
	i.peers[peer.Peer] = peer
}

// Lookup returns ownership only when evidence is unambiguous. complete is
// false after capacity exhaustion: a partial index cannot justify eviction.
func (i *AccessIndex) Lookup(peer netip.AddrPort) (owner AccessPeer, known, complete bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	owner, known = i.peers[peer]
	return owner, known, !i.overflow
}

func parseAccessPeer(line string, now time.Time) (AccessPeer, bool) {
	fields := strings.Fields(line)
	var address, email string
	for n := 0; n+2 < len(fields); n++ {
		if fields[n] == "from" && fields[n+2] == "accepted" {
			address = fields[n+1]
			break
		}
	}
	if address == "" || strings.HasPrefix(address, "udp:") {
		return AccessPeer{}, false
	}
	address = strings.TrimPrefix(address, "tcp:")
	peer, err := netip.ParseAddrPort(address)
	if err != nil || peer.Port() == 0 || peer.Addr().IsUnspecified() || peer.Addr().IsMulticast() {
		return AccessPeer{}, false
	}
	for n := 0; n+1 < len(fields); n++ {
		if fields[n] == "email:" {
			email = fields[n+1]
			break
		}
	}
	credential, suffix, ok := strings.Cut(email, "@")
	if !ok || suffix != "proxima" {
		return AccessPeer{}, false
	}
	id, err := uuid.Parse(credential)
	if err != nil {
		return AccessPeer{}, false
	}
	peer = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port())
	return AccessPeer{UUID: id.String(), Peer: peer, AcceptedAt: now}, true
}
