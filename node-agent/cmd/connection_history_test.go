package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/connectionlog"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestConnectionHistoryPreservesRuntimeAcrossSupplierReplacement(t *testing.T) {
	// Given a real exporter, a retained owner, and the actual agent HTTP client.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("/tmp", "history-i-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	var mu sync.Mutex
	policy := client.ConnectionHistoryConfig{SchemaVersion: 1, Bindings: []nodeprov.HistoryBinding{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Node-Key") != "test-key" {
			w.WriteHeader(401)
			return
		}
		if r.Method == http.MethodGet {
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(policy)
			return
		}
		var batch client.ConnectionHistoryBatch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(client.ConnectionHistoryACK{BatchID: batch.BatchID, PayloadSHA256: batch.PayloadSHA256, Status: "committed"})
	}))
	defer server.Close()
	api := client.NewAPIClient(&config.AgentConfig{ServerURL: server.URL, NodeID: "node", APIKey: "test-key"})
	history := newConnectionHistory(ctx, api, filepath.Join(dir, "config.json"))
	observers := map[string]*connectionhistory.Observer{}
	for _, runtime := range []string{"xray", "hysteria2"} {
		observer, err := connectionhistory.NewObserver(connectionhistory.Options{ProducerID: runtime, Runtime: runtime, RuntimeBuild: "integration", Capabilities: []string{"hot_toggle"}})
		if err != nil {
			t.Fatal(err)
		}
		exporter, err := observer.Listen(ctx, history.socket(runtime))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := exporter.Close(); err != nil {
				t.Error(err)
			}
		})
		observers[runtime] = observer
	}
	t.Cleanup(func() {
		if err := history.Close(); err != nil {
			t.Error(err)
		}
	})
	tunnel, err := observers["xray"].NewTunnel(connectionhistory.TunnelInfo{Protocol: "vless", InboundTag: "vless", LabelKind: "xray_email", Label: "private-label"})
	if err != nil {
		t.Fatal(err)
	}
	boot, owner := observers["xray"].BootID(), tunnel.ID()
	wait := func(manager *connectionlog.Manager, state string) {
		t.Helper()
		for {
			changed := manager.Changes()
			if manager.Status().State == state {
				return
			}
			select {
			case <-changed:
			case <-ctx.Done():
				t.Fatalf("status %s: %+v", state, manager.Status())
			}
		}
	}
	if err := history.sync([]client.InboundConfig{{Protocol: "vless_reality", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	wait(history.manager, "off")
	epoch, err := nodeprov.NewHistoryUUID()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	policy.Enabled, policy.Revision, policy.CollectionEpoch = true, 1, &epoch
	mu.Unlock()
	if err := history.manager.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	wait(history.manager, "collecting")
	first := history.manager
	// When another supplier is enabled, only the collector is reopened.
	inbounds := []client.InboundConfig{{Protocol: "vless_reality", Enabled: true}, {Protocol: "hysteria2", Enabled: true}}
	if err := history.sync(inbounds); err != nil {
		t.Fatal(err)
	}
	wait(history.manager, "collecting")
	if first == history.manager {
		t.Fatal("supplier set was not replaced")
	}
	if observers["xray"].BootID() != boot || tunnel.ID() != owner {
		t.Fatal("runtime owner changed")
	}
	second := history.manager
	if err := history.sync(inbounds); err != nil {
		t.Fatal(err)
	}
	if history.manager != second {
		t.Fatal("unchanged suppliers reopened collector")
	}
}
