package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestConnectionHistoryHTTPConfiguredXrayProtocolCapabilities(t *testing.T) {
	for _, protocol := range []struct {
		configured string
		event      string
		mux        bool
	}{
		{configured: "vless_reality", event: "vless", mux: true},
		{configured: "vmess_ws", event: "vmess", mux: true},
		{configured: "trojan_tls", event: "trojan"},
	} {
		t.Run(protocol.configured, func(t *testing.T) {
			// Given: the exact protocol name admitted by admin_inbound.go and
			// dispatched by xray_config.go, not its canonical event protocol.
			f := historyFixtureWithProtocol(t, protocol.configured)
			caps := []string{"auth_transport", "hot_toggle", "tcp_flow", "udp_destination"}
			if protocol.mux {
				caps = []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"}
			}
			status := services.ConnectionHistoryStatus{
				State: "off",
				Producers: []services.ConnectionHistoryProducerStatus{{
					ProducerID: "xray", ObserverBootID: &f.boot, RuntimeBuild: "fixture-probed-build", Capabilities: caps,
				}},
			}
			if err := f.svc.RecordStatus(context.Background(), f.node, status); err != nil {
				t.Fatal(err)
			}
			var stored string
			if err := f.db.QueryRow(context.Background(), `SELECT protocol FROM inbounds WHERE node_id=$1`, f.node).Scan(&stored); err != nil || stored != protocol.configured {
				t.Fatalf("configured protocol=%q want=%q err=%v", stored, protocol.configured, err)
			}
			cmd := services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node)}}, Enabled: true}
			// When: actual authenticated bulk control enables the production-shaped node.
			f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 200)
			// Then: config and applied status agree, while event protocol stays canonical.
			body := f.request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, "history-key-0", 200)
			if err := json.Unmarshal(body, &f.config); err != nil {
				t.Fatal(err)
			}
			if !f.config.Enabled || f.config.Revision != 1 || f.config.CollectionEpoch == nil {
				t.Fatalf("config=%s", body)
			}
			status.DesiredRevision, status.AppliedRevision = 1, 1
			status.CollectionEpoch, status.Enabled, status.State = f.config.CollectionEpoch, true, "collecting"
			status.Producers[0].AppliedRevision = 1
			if err := f.svc.RecordStatus(context.Background(), f.node, status); err != nil {
				t.Fatalf("collecting for configured %s: %v", protocol.configured, err)
			}
			event := f.event(1, "flow_end", 1, 127, 263)
			event.Protocol = &protocol.event
			event.Auth = nodeprov.HistoryAuth{Kind: "unknown"}
			event.Quality.Identity = "unknown"
			f.ingest(f.batch(event), 200)
			page := f.page("?protocol=" + protocol.event)
			if len(page.Items) != 1 || *page.Items[0].Event.Protocol != protocol.event {
				t.Fatalf("canonical event protocol changed: %+v", page)
			}
		})
	}
}

func TestConnectionHistoryHTTPConfiguredMuxProtocolsRequireBothHooks(t *testing.T) {
	for _, configured := range []string{"vless_reality", "vmess_ws"} {
		for _, missing := range []string{"mux", "xudp"} {
			t.Run(configured+"/missing_"+missing, func(t *testing.T) {
				// Given: production protocol, with precisely one required hook absent.
				f := historyFixtureWithProtocol(t, configured)
				var caps []string
				for _, capability := range []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"} {
					if capability != missing {
						caps = append(caps, capability)
					}
				}
				status := services.ConnectionHistoryStatus{State: "off", Producers: []services.ConnectionHistoryProducerStatus{{
					ProducerID: "xray", ObserverBootID: &f.boot, RuntimeBuild: "fixture-probed-build", Capabilities: caps,
				}}}
				if err := f.svc.RecordStatus(context.Background(), f.node, status); err != nil {
					t.Fatal(err)
				}
				f.status(f.other)
				cmd := services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{
					{ID: nodeprov.HistoryUUID(f.node)}, {ID: nodeprov.HistoryUUID(f.other)},
				}, Enabled: true}
				// When: the mixed-node bulk command encounters the missing hook.
				body := f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
				// Then: the configured protocol is named and every node remains OFF.
				if !bytes.Contains(body, []byte("unsupported:"+configured)) {
					t.Fatalf("missing configured-protocol reason: %s", body)
				}
				var changed int
				if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM connection_log_controls WHERE enabled OR revision<>0`).Scan(&changed); err != nil || changed != 0 {
					t.Fatalf("bulk changed=%d err=%v", changed, err)
				}
			})
		}
	}
}
