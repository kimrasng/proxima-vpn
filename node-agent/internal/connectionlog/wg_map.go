package connectionlog

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const wgMaxPeers = 1024

func wgHostMap(peers []WireGuardPeer) (map[netip.Addr]string, error) {
	if len(peers) > wgMaxPeers {
		return nil, ErrWireGuardMap
	}
	hosts := make(map[netip.Addr]string)
	keys := make(map[string]bool)
	for _, peer := range peers {
		key, err := base64.StdEncoding.DecodeString(peer.PublicKey)
		if err != nil || len(key) != 32 || keys[peer.PublicKey] {
			return nil, ErrWireGuardMap
		}
		keys[peer.PublicKey] = true
		for _, prefix := range peer.AllowedIPs {
			addr := prefix.Addr()
			if !prefix.IsValid() || prefix.Bits() != addr.BitLen() || addr.Is4In6() || addr.IsUnspecified() || addr.IsMulticast() {
				return nil, ErrWireGuardMap
			}
			if _, exists := hosts[addr]; exists {
				return nil, ErrWireGuardMap
			}
			hosts[addr] = peer.PublicKey
			if len(hosts) > wgMaxPeers*16 {
				return nil, ErrWireGuardMap
			}
		}
	}
	return hosts, nil
}

// readWGApplied reads public keys and allowed IPs only. No private key, PSK,
// handshake timestamp, endpoint, or counters are requested from wg.
func readWGApplied(ctx context.Context, name string) (WireGuardAppliedMap, error) {
	if runtime.GOOS != "linux" {
		return WireGuardAppliedMap{}, ErrWireGuardUnsupported
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return WireGuardAppliedMap{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wg", "show", name, "allowed-ips").Output()
	if err != nil {
		return WireGuardAppliedMap{}, fmt.Errorf("read applied WireGuard map: %w", err)
	}
	result := WireGuardAppliedMap{InterfaceIndex: iface.Index}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			return WireGuardAppliedMap{}, ErrWireGuardMap
		}
		peer := WireGuardPeer{PublicKey: fields[0]}
		for _, field := range fields[1:] {
			for _, raw := range strings.Split(field, ",") {
				if raw == "" || raw == "(none)" {
					continue
				}
				prefix, err := netip.ParsePrefix(raw)
				if err != nil {
					return WireGuardAppliedMap{}, ErrWireGuardMap
				}
				peer.AllowedIPs = append(peer.AllowedIPs, prefix)
			}
		}
		result.Peers = append(result.Peers, peer)
	}
	after, err := net.InterfaceByName(name)
	if err != nil || after.Index != iface.Index {
		return WireGuardAppliedMap{}, ErrWireGuardRecreated
	}
	return result, nil
}

func (w *WireGuardObserver) verifyMap(ctx context.Context) error {
	actual, err := readWGApplied(ctx, w.options.Interface)
	if err != nil {
		return err
	}
	if actual.InterfaceIndex != w.index {
		return ErrWireGuardRecreated
	}
	hosts, err := wgHostMap(actual.Peers)
	if err != nil {
		return err
	}
	if !maps.Equal(w.hosts, hosts) {
		return ErrWireGuardMap
	}
	return nil
}

func (w *WireGuardObserver) FreezeAppliedMap(ctx context.Context) error {
	w.op.Lock()
	defer w.op.Unlock()
	if w.closed {
		return ErrWireGuardClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.freeze()
}

func (w *WireGuardObserver) freeze() error {
	defer w.notifyProducer()
	w.mu.Lock()
	w.frozen = true
	clear(w.gaps)
	err := w.gap("identity_map_changed")
	w.mu.Unlock()
	err = errors.Join(err, w.stopCapture())
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(err, w.clearWindows("peer_removed"))
}

// PublishAppliedMap must follow FreezeAppliedMap before any mutation. A failed
// or partial reconcile publishes only the actual successful configuration.
func (w *WireGuardObserver) PublishAppliedMap(ctx context.Context, applied WireGuardAppliedMap) error {
	w.op.Lock()
	defer w.op.Unlock()
	if w.closed {
		return ErrWireGuardClosed
	}
	if !w.frozen {
		return fmt.Errorf("%w: freeze before publish", ErrWireGuardMap)
	}
	hosts, err := wgHostMap(applied.Peers)
	if err != nil {
		return err
	}
	actual, err := readWGApplied(ctx, w.options.Interface)
	if err != nil {
		return err
	}
	if applied.InterfaceIndex != actual.InterfaceIndex || (w.index != 0 && w.index != actual.InterfaceIndex) {
		return ErrWireGuardRecreated
	}
	kernelHosts, err := wgHostMap(actual.Peers)
	if err != nil || !maps.Equal(hosts, kernelHosts) {
		return ErrWireGuardMap
	}
	w.mu.Lock()
	w.hosts, w.index = hosts, applied.InterfaceIndex
	w.stats.MapGeneration++
	w.frozen = false
	clear(w.fragments)
	clear(w.gaps)
	w.stats.Degraded = false
	w.lastCheck = w.now()
	w.mu.Unlock()
	if w.enabled {
		if err := w.startCapture(); err != nil {
			w.mu.Lock()
			w.frozen = true
			w.mu.Unlock()
			return err
		}
	}
	return nil
}
