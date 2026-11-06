package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/connectionlog"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/process"
)

// connectionHistory replaces only collector workers when the required supplier
// set changes. The durable directory and all VPN processes survive replacement.
type connectionHistory struct {
	mu              sync.Mutex
	ctx             context.Context
	api             *client.APIClient
	directory       string
	socketDirectory string
	manager         *connectionlog.Manager
	cancel          context.CancelFunc
	done            chan error
	required        []string
	failure         string
	hysteriaShared  bool
	wg              *connectionlog.WireGuardObserver
	wgIndex         int
	wgCancel        context.CancelFunc
	wgDone          chan error
}

func newConnectionHistory(ctx context.Context, api *client.APIClient, configPath string) *connectionHistory {
	return &connectionHistory{ctx: ctx, api: api,
		directory:       filepath.Join(filepath.Dir(configPath), "connection-history"),
		socketDirectory: filepath.Join(filepath.Dir(configPath), "history-run")}
}

func (h *connectionHistory) socket(runtime string) string {
	return filepath.Join(h.socketDirectory, runtime+".sock")
}

func (h *connectionHistory) stopManager() error {
	if h.manager == nil {
		return nil
	}
	h.cancel()
	err := <-h.done
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	err = errors.Join(err, h.manager.Close())
	h.manager = nil
	return err
}

func (h *connectionHistory) sync(inbounds []client.InboundConfig) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	required := []string{}
	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		id := ""
		switch ib.Protocol {
		case "vless_reality", "vmess_ws", "trojan_tls", "shadowsocks":
			id = "xray"
		case "hysteria2":
			id = "hysteria2"
		case "wireguard":
			id = "wireguard:wg0"
		}
		if id != "" && !slices.Contains(required, id) {
			required = append(required, id)
		}
	}
	slices.Sort(required)
	if h.manager != nil && slices.Equal(required, h.required) {
		return nil
	}
	if err := h.stopManager(); err != nil {
		h.failure = "collector_close_failed"
		return err
	}
	manager, err := connectionlog.NewManager(connectionlog.Options{Directory: h.directory, API: h.api})
	if err != nil {
		h.failure = "outbox_unavailable"
		return err
	}
	for _, id := range required {
		var source connectionlog.Producer
		if strings.HasPrefix(id, "wireguard:") {
			if h.wg == nil {
				h.failure = "wireguard_map_unavailable"
				return errors.Join(fmt.Errorf("WireGuard applied map unavailable"), manager.Close())
			}
			source = h.wg
		} else {
			source = connectionlog.NewUnixSource(id, h.socket(id))
		}
		if err := manager.Register(source); err != nil {
			return errors.Join(err, manager.Close())
		}
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.manager, h.cancel, h.done, h.required = manager, cancel, make(chan error, 1), required
	h.failure = ""
	done := h.done
	go func() { done <- manager.Run(ctx) }()
	return nil
}

func (h *connectionHistory) status() json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	status := connectionlog.Status{State: "error", ErrorCode: h.failure, Producers: []connectionlog.ProducerStatus{}}
	if h.manager != nil {
		status = h.manager.Status()
		if len(h.required) == 0 {
			status.State, status.ErrorCode = "unsupported", "no_local_suppliers"
		}
		if h.hysteriaShared && status.State == "collecting" {
			status.State, status.ErrorCode = "degraded", "shared_identity"
		}
	}
	if h.wg != nil && slices.Contains(h.required, "wireguard:wg0") {
		wgStatus := h.wg.Stats()
		if wgStatus.Degraded || (status.Enabled && !wgStatus.Capturing) {
			status.State, status.ErrorCode = "degraded", "wireguard_capture_unavailable"
		}
	}
	if h.failure != "" {
		status.State, status.ErrorCode = "degraded", h.failure
	}
	data, err := json.Marshal(status)
	if err != nil {
		log.Printf("connection history status: %v", err)
		return nil
	}
	return data
}

func (h *connectionHistory) setHysteriaShared(shared bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hysteriaShared = shared
}

func (h *connectionHistory) freezeWireGuard(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wg == nil {
		return nil
	}
	return h.wg.FreezeAppliedMap(ctx)
}

func (h *connectionHistory) publishWireGuard(ctx context.Context, manager *process.WireGuardManager) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	index, peers, err := manager.AppliedPeers(ctx)
	if err != nil {
		h.failure = "wireguard_map_unavailable"
		return err
	}
	observerStopped := false
	if h.wg != nil {
		select {
		case runErr := <-h.wgDone:
			observerStopped = true
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				log.Printf("connection history WireGuard observer: %v", runErr)
			}
		default:
		}
	}
	if h.wg == nil || h.wgIndex != index || observerStopped {
		if err := h.stopManager(); err != nil {
			return err
		}
		if h.wg != nil {
			h.wgCancel()
			if err := <-h.wgDone; err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("connection history WireGuard replacement: %v", err)
			}
			if err := h.wg.Close(); err != nil {
				return err
			}
		}
		h.wg, err = connectionlog.NewWireGuardObserver(connectionlog.WireGuardOptions{Interface: "wg0", RuntimeBuild: "node-agent-" + version})
		if err != nil {
			return err
		}
		h.wgIndex = index
		runCtx, cancel := context.WithCancel(h.ctx)
		h.wgCancel, h.wgDone = cancel, make(chan error, 1)
		observer, done := h.wg, h.wgDone
		go func() {
			done <- observer.Run(runCtx)
			close(done)
		}()
	}
	if !h.wg.Stats().Frozen {
		return nil
	}
	applied := connectionlog.WireGuardAppliedMap{InterfaceIndex: index, Peers: []connectionlog.WireGuardPeer{}}
	for _, peer := range peers {
		p := connectionlog.WireGuardPeer{PublicKey: peer.PublicKey}
		for _, raw := range strings.Split(peer.AllowedIPs, ",") {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("parse applied WireGuard prefix: %w", err)
			}
			p.AllowedIPs = append(p.AllowedIPs, prefix)
		}
		applied.Peers = append(applied.Peers, p)
	}
	if err := h.wg.PublishAppliedMap(ctx, applied); err != nil {
		h.failure = "wireguard_map_unavailable"
		return err
	}
	h.failure = ""
	return nil
}

func (h *connectionHistory) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	err := h.stopManager()
	if h.wg != nil {
		h.wgCancel()
		if runErr := <-h.wgDone; runErr != nil && !errors.Is(runErr, context.Canceled) {
			err = errors.Join(err, runErr)
		}
		err = errors.Join(err, h.wg.Close())
		h.wg = nil
	}
	return err
}
