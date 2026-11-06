package connectionlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// The owning API test supplies production routes and an isolated PostgreSQL DB.
// This process owns the real exporter socket and SIGKILLs a separate collector.
func TestHistoryOFFRecoveryRealHTTP(t *testing.T) {
	base := os.Getenv("HISTORY_B1_SERVER")
	if base == "" {
		t.Skip("run TestHistoryOFFRecoveryHTTP in the API subscription package")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	nodePath := "/api/v1/nodes/" + os.Getenv("HISTORY_B1_NODE")
	request := func(method, path string, payload any, target any) {
		t.Helper()
		var body []byte
		var err error
		if payload != nil {
			body, err = json.Marshal(payload)
			must(t, err)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
		must(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-Key", "b1-key")
		req.Header.Set("Authorization", "Bearer "+os.Getenv("HISTORY_B1_ADMIN"))
		res, err := http.DefaultClient.Do(req)
		must(t, err)
		data, err := io.ReadAll(res.Body)
		must(t, err)
		must(t, res.Body.Close())
		t.Logf("B1_HTTP %s %s status=%d body=%s", method, path, res.StatusCode, data)
		if res.StatusCode != 200 {
			t.Fatalf("production HTTP rejected request: %s", data)
		}
		if target != nil {
			must(t, json.Unmarshal(data, target))
		}
	}
	var cfg client.ConnectionHistoryConfig
	request("GET", nodePath+"/connection-history/config", nil, &cfg)
	toggle := func(enabled bool) {
		t.Helper()
		command := map[string]any{"enabled": enabled, "nodes": []map[string]any{{
			"id": os.Getenv("HISTORY_B1_NODE"), "expected_revision": cfg.Revision,
		}}}
		request("POST", "/api/v1/admin/nodes/connection-logging", command, nil)
		request("GET", nodePath+"/connection-history/config", nil, &cfg)
	}
	root, err := os.MkdirTemp("", "b1-")
	must(t, err)
	t.Cleanup(func() { must(t, os.RemoveAll(root)) })
	socket := filepath.Join(root, "s")
	newObserver := func() (*connectionhistory.Observer, *connectionhistory.Server) {
		t.Helper()
		obs, err := connectionhistory.NewObserver(connectionhistory.Options{
			ProducerID: "xray", Runtime: "xray", RuntimeBuild: "b1-shared-exporter",
			Capabilities: []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"},
		})
		must(t, err)
		server, err := obs.Listen(ctx, socket)
		must(t, err)
		return obs, server
	}
	obs, server := newObserver()
	defer func() { must(t, server.Close()) }()
	// Given ON -> actual applied OFF, with old detail still in the agent outbox.
	toggle(true)
	firstEpoch := *cfg.CollectionEpoch
	agent := startOFFRecoveryProcess(t, ctx, filepath.Join(root, "outbox"), socket)
	if s := agent.receive(t, ctx); s.State != "collecting" {
		t.Fatalf("ON not applied: %+v", s)
	}
	tunnel, err := obs.NewTunnel(connectionhistory.TunnelInfo{Protocol: "vless",
		InboundTag: "b1", LabelKind: "xray_email", Label: "unknown-authenticated-owner"})
	must(t, err)
	host, port := "b1.example.test", 443
	flow, err := obs.NewFlow(connectionhistory.FlowInfo{Tunnel: tunnel,
		Destination: nodeprov.HistoryDestination{RequestedHost: &host, RequestedPort: &port, Transport: "tcp"}})
	must(t, err)
	must(t, flow.Observe(17, 29))
	toggle(false)
	off := agent.request(t, ctx, "poll")
	if off.Enabled || off.State != "off" || off.AppliedRevision != cfg.Revision || off.QueueBytes == 0 {
		t.Fatalf("OFF checkpoint not established: %+v", off)
	}
	offSequence, oldBoot := obs.Hello().LastSequence, obs.BootID()
	raw, err := json.Marshal(off)
	must(t, err)
	fmt.Printf("B1_OFF_STATUS %s\n", raw)
	// When an unclean agent restart occurs after the durable OFF barrier.
	agent.kill(t)
	if os.Getenv("HISTORY_B1_RESTART_PRODUCER") == "1" {
		must(t, server.Close())
		obs, server = newObserver()
	}
	agent = startOFFRecoveryProcess(t, ctx, filepath.Join(root, "outbox"), socket)
	recovered := agent.receive(t, ctx)
	if recovered.Enabled || recovered.AppliedRevision != cfg.Revision {
		t.Fatalf("recovered OFF not applied: %+v", recovered)
	}
	// Then genuine old detail drains through strict production admission.
	for range 4 {
		recovered = agent.request(t, ctx, "upload")
		if recovered.QueueBytes == 0 {
			break
		}
	}
	if recovered.QueueBytes != 0 || recovered.State != "off" {
		t.Fatalf("OFF recovery poisoned delivery: %+v", recovered)
	}
	raw, err = json.Marshal(recovered)
	must(t, err)
	fmt.Printf("B1_OFF_STATUS %s\n", raw)
	if os.Getenv("HISTORY_B1_RESTART_PRODUCER") == "1" {
		if obs.BootID() == oldBoot || obs.Hello().LastSequence != 0 || obs.Hello().Enabled {
			t.Fatalf("new OFF producer falsely collected: %+v", obs.Hello())
		}
		// An additional agent restart must preserve idempotent initial local OFF.
		agent.kill(t)
		agent = startOFFRecoveryProcess(t, ctx, filepath.Join(root, "outbox"), socket)
		if s := agent.receive(t, ctx); s.State != "off" || s.AppliedRevision != cfg.Revision {
			t.Fatalf("normalized OFF did not survive reconnect: %+v", s)
		}
	} else {
		must(t, flow.Observe(900, 800))
		must(t, obs.Snapshot())
		if obs.BootID() != oldBoot || obs.Hello().LastSequence != offSequence {
			t.Fatal("OFF recovery restarted or collected from surviving producer")
		}
	}
	// Reenable uses the next issued epoch and must not get stuck behind the gap.
	toggle(true)
	if cfg.CollectionEpoch == nil || *cfg.CollectionEpoch == firstEpoch {
		t.Fatal("reenable did not issue a fresh interval")
	}
	on := agent.request(t, ctx, "poll")
	if on.State != "collecting" || on.AppliedRevision != cfg.Revision {
		t.Fatalf("reenable failed: %+v", on)
	}
	on = agent.request(t, ctx, "upload")
	if on.QueueBytes != 0 {
		t.Fatalf("new epoch failed to drain: %+v", on)
	}
	raw, err = json.Marshal(on)
	must(t, err)
	fmt.Printf("B1_ON_STATUS %s\n", raw)
	agent.kill(t)
}
