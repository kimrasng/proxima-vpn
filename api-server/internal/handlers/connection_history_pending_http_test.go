package handlers_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/middleware"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func historyPendingFixture(t *testing.T) (*historyHTTPFixture, services.ConnectionHistoryConfig, services.ConnectionHistoryStatus) {
	t.Helper()
	f := historyFixture(t)
	h := handlers.NewNodeAgentHandler(f.db, nil, services.ManagedEntryDNSIntentConfig{})
	f.app.Post("/api/v1/nodes/:id/heartbeat", middleware.NodeAPIKeyMiddleware(f.db), h.Heartbeat)
	cfg := historyPendingBinding(f, 0, "initial")
	status := historyPendingStatus(f, cfg)
	historyPendingHeartbeat(f, status, 200)
	historyPendingAssert(f, cfg, status, "off")
	return f, cfg, status
}

func historyPendingBinding(f *historyHTTPFixture, revision nodeprov.HistoryU64, label string) services.ConnectionHistoryConfig {
	f.t.Helper()
	_, err := f.svc.IssueBinding(context.Background(), f.node, revision,
		nodeprov.HistoryBinding{Runtime: "xray", InboundTag: "vless-test", LabelKind: "xray_email", Label: label, Kind: "device"},
		services.ConnectionHistoryBindingOwner{UserID: &f.user, DeviceID: &f.device})
	if err != nil {
		f.t.Fatal(err)
	}
	return historyPendingConfig(f)
}

func historyPendingConfig(f *historyHTTPFixture) services.ConnectionHistoryConfig {
	f.t.Helper()
	var cfg services.ConnectionHistoryConfig
	raw := f.request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, "history-key-0", 200)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		f.t.Fatal(err)
	}
	return cfg
}

func historyPendingToggle(f *historyHTTPFixture, cfg services.ConnectionHistoryConfig, enabled bool) services.ConnectionHistoryConfig {
	f.t.Helper()
	f.request("POST", "/api/v1/admin/nodes/connection-logging", services.ConnectionHistoryBulkCommand{
		Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: cfg.Revision}}, Enabled: enabled,
	}, "", 200)
	return historyPendingConfig(f)
}

func historyPendingStatus(f *historyHTTPFixture, cfg services.ConnectionHistoryConfig) services.ConnectionHistoryStatus {
	state := "off"
	if cfg.Enabled {
		state = "collecting"
	}
	return services.ConnectionHistoryStatus{
		DesiredRevision: cfg.Revision, AppliedRevision: cfg.Revision, CollectionEpoch: cfg.CollectionEpoch,
		Enabled: cfg.Enabled, State: state, AppliedAt: historyPtr(nodeprov.HistoryTimestamp(f.now)),
		Producers: []services.ConnectionHistoryProducerStatus{{
			ProducerID: "xray:main", ObserverBootID: &f.boot, RuntimeBuild: "pending-http-history-v1",
			Capabilities:    []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"},
			AppliedRevision: cfg.Revision,
		}},
	}
}

func historyPendingHeartbeat(f *historyHTTPFixture, status services.ConnectionHistoryStatus, code int) []byte {
	f.t.Helper()
	return f.request("POST", "/api/v1/nodes/"+f.node+"/heartbeat",
		map[string]any{"connection_logging": status}, "history-key-0", code)
}

func historyPendingAssert(f *historyHTTPFixture, desired services.ConnectionHistoryConfig, applied services.ConnectionHistoryStatus, state string) {
	f.t.Helper()
	var got struct {
		State                  string                                     `json:"state"`
		DesiredRevision        nodeprov.HistoryU64                        `json:"desired_revision"`
		AppliedRevision        nodeprov.HistoryU64                        `json:"applied_revision"`
		Enabled                bool                                       `json:"enabled"`
		AppliedEnabled         bool                                       `json:"applied_enabled"`
		CollectionEpoch        *nodeprov.HistoryUUID                      `json:"collection_epoch"`
		AppliedCollectionEpoch *nodeprov.HistoryUUID                      `json:"applied_collection_epoch"`
		ErrorCode              string                                     `json:"error_code"`
		Producers              []services.ConnectionHistoryProducerStatus `json:"producers"`
	}
	raw := f.request("GET", "/api/v1/admin/nodes/"+f.node+"/connection-logging", nil, "", 200)
	if err := json.Unmarshal(raw, &got); err != nil {
		f.t.Fatal(err)
	}
	if got.State != state || got.DesiredRevision != desired.Revision || got.AppliedRevision != applied.AppliedRevision ||
		got.Enabled != desired.Enabled || got.AppliedEnabled != applied.Enabled ||
		!reflect.DeepEqual(got.CollectionEpoch, desired.CollectionEpoch) ||
		!reflect.DeepEqual(got.AppliedCollectionEpoch, applied.CollectionEpoch) ||
		got.ErrorCode != applied.ErrorCode || !reflect.DeepEqual(got.Producers, applied.Producers) {
		f.t.Fatalf("want state=%s desired=%s applied=%s with unchanged applied evidence; got %s",
			state, desired.Revision, applied.AppliedRevision, raw)
	}
}

func TestConnectionHistoryHTTPPendingDelayedHeartbeat(t *testing.T) {
	for _, scenario := range []string{"old_off_after_on", "old_on_after_off", "same_epoch_new_revision", "different_epoch", "off_new_revision"} {
		t.Run(scenario, func(t *testing.T) {
			// Given: real issued revision 1, authenticated applied OFF, then a
			// committed policy change before delivery of the old heartbeat.
			f, cfg, old := historyPendingFixture(t)
			code := 200
			switch scenario {
			case "old_off_after_on":
				cfg = historyPendingToggle(f, cfg, true)
			case "old_on_after_off", "same_epoch_new_revision", "different_epoch":
				cfg = historyPendingToggle(f, cfg, true)
				old = historyPendingStatus(f, cfg)
				historyPendingHeartbeat(f, old, 200)
				if scenario == "same_epoch_new_revision" {
					cfg = historyPendingBinding(f, cfg.Revision, "additional")
					if !reflect.DeepEqual(cfg.CollectionEpoch, old.CollectionEpoch) {
						t.Fatal("binding revision changed collection epoch")
					}
				} else {
					cfg = historyPendingToggle(f, cfg, false)
				}
				code = 409 // Stale collecting cannot bypass the supplier barrier.
				if scenario == "different_epoch" {
					old = historyPendingStatus(f, cfg)
					historyPendingHeartbeat(f, old, 200)
					cfg = historyPendingToggle(f, cfg, true)
					if reflect.DeepEqual(cfg.CollectionEpoch, old.CollectionEpoch) {
						t.Fatal("re-enable reused the closed epoch")
					}
					code = 200
				}
			case "off_new_revision":
				cfg = historyPendingBinding(f, cfg.Revision, "additional")
			}
			historyPendingAssert(f, cfg, old, "pending")

			// When: release the delayed heartbeat after the command response.
			// Synchronous HTTP completion provides ordering without sleeps.
			raw := historyPendingHeartbeat(f, old, code)
			if code == 409 {
				var failure struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(raw, &failure); err != nil || failure.Error != "not_collecting" {
					t.Fatalf("stale collecting response=%s err=%v", raw, err)
				}
			}

			// Then: the admin sees pending, not the old OFF/ON state; only an
			// acknowledgement of the current issued tuple settles the command.
			historyPendingAssert(f, cfg, old, "pending")
			current := historyPendingStatus(f, cfg)
			historyPendingHeartbeat(f, current, 200)
			historyPendingAssert(f, cfg, current, current.State)
		})
	}
}

func TestConnectionHistoryHTTPPendingPreservesFailureAndOffline(t *testing.T) {
	for _, state := range []string{"error", "degraded", "unsupported", "offline"} {
		t.Run(state, func(t *testing.T) {
			// Given: a pending enable and an applied OFF tuple.
			f, cfg, old := historyPendingFixture(t)
			cfg = historyPendingToggle(f, cfg, true)
			// When: a real diagnostic heartbeat arrives, or the node becomes
			// offline. Set an absolute old timestamp; do not wait for expiry.
			if state != "offline" {
				old.State, old.ErrorCode = state, "export_unavailable"
				old.Producers[0].ErrorCode = "export_unavailable"
			}
			historyPendingHeartbeat(f, old, 200)
			if state == "offline" {
				if _, err := f.db.Exec(context.Background(), `UPDATE nodes SET last_seen='2000-01-01T00:00:00Z' WHERE id=$1`, f.node); err != nil {
					t.Fatal(err)
				}
			}
			// Then: pending must not mask a genuine diagnostic or offline state.
			historyPendingAssert(f, cfg, old, state)
		})
	}
}

func TestConnectionHistoryHTTPPendingRequiresAllSuppliers(t *testing.T) {
	// Given: desired ON, but the second supplier has not applied its revision.
	f, cfg, old := historyPendingFixture(t)
	cfg = historyPendingToggle(f, cfg, true)
	current := historyPendingStatus(f, cfg)
	second := current.Producers[0]
	second.ProducerID, second.AppliedRevision = "xray:second", old.AppliedRevision
	current.Producers = append(current.Producers, second)
	// When: the node tries to acknowledge collecting with incomplete evidence.
	raw := historyPendingHeartbeat(f, current, 409)
	// Then: rejection leaves the prior applied tuple and pending command intact.
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &failure); err != nil || failure.Error != "supplier_not_applied" {
		t.Fatalf("supplier barrier response=%s err=%v", raw, err)
	}
	historyPendingAssert(f, cfg, old, "pending")
	current.Producers[1].AppliedRevision = cfg.Revision
	historyPendingHeartbeat(f, current, 200)
	historyPendingAssert(f, cfg, current, "collecting")
}
