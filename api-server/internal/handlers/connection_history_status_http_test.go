package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestConnectionHistoryHTTPAgentStatusWireAndUnavailableProducer(t *testing.T) {
	// Given: the agent's exact Status JSON, including an unavailable source.
	f := historyFixture(t)
	unavailable := []byte(`{"desired_revision":"0","applied_revision":"0","collection_epoch":null,"enabled":false,"state":"unsupported","applied_at":null,"last_config_fetch_at":null,"queue_bytes":"0","dropped_events":"0","error_code":"export_unavailable","producers":[{"producer_id":"xray:main","observer_boot_id":null,"runtime_build":"","capabilities":null,"applied_revision":"0","last_durable_sequence":"0","error_code":"export_unavailable"}]}`)
	t.Logf("AGENT STATUS INPUT before decode and database write: %s", unavailable)
	status, err := services.DecodeConnectionHistoryStatus(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RecordStatus(context.Background(), f.node, status); err != nil {
		t.Fatal(err)
	}
	cmd := services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node)}}, Enabled: true}
	body := f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
	if !bytes.Contains(body, []byte("unsupported:vless_reality")) {
		t.Fatalf("missing unsupported: %s", body)
	}
	off := []byte(fmt.Sprintf(`{"desired_revision":"0","applied_revision":"0","collection_epoch":null,"enabled":false,"state":"off","applied_at":null,"last_config_fetch_at":null,"queue_bytes":"0","dropped_events":"0","error_code":"","producers":[{"producer_id":"xray:main","observer_boot_id":"%s","runtime_build":"fixture-probed-build","capabilities":["auth_transport","hot_toggle","mux","tcp_flow","udp_destination","xudp"],"applied_revision":"0","last_durable_sequence":"0","error_code":""}]}`, f.boot))
	t.Logf("AGENT STATUS INPUT before decode and database write: %s", off)
	status, err = services.DecodeConnectionHistoryStatus(off)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RecordStatus(context.Background(), f.node, status); err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 200)
	cfg, err := f.svc.Config(context.Background(), f.node)
	if err != nil {
		t.Fatal(err)
	}
	status.DesiredRevision = 1
	status.AppliedRevision = 1
	status.CollectionEpoch = cfg.CollectionEpoch
	status.Enabled = true
	status.State = "collecting"
	status.Producers[0].AppliedRevision = 1
	status.AppliedAt = historyPtr(nodeprov.HistoryTimestamp(f.now))
	status.QueueBytes = nodeprov.HistoryU64(^uint64(0))
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AGENT STATUS INPUT before decode and database write: %s", raw)
	// When: the heartbeat integration decodes and records the actual wire shape.
	status, err = services.DecodeConnectionHistoryStatus(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RecordStatus(context.Background(), f.node, status); err != nil {
		t.Fatal(err)
	}
	// Then: admin sees collecting and lossless U64, without requiring shortened keys.
	body = f.request("GET", "/api/v1/admin/nodes/"+f.node+"/connection-logging", nil, "", 200)
	for _, want := range []string{`"state":"collecting"`, `"producer_id":"xray:main"`, `"observer_boot_id":`, `"runtime_build":"fixture-probed-build"`, `"queue_bytes":"18446744073709551615"`, `"error_code":""`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %s: %s", want, body)
		}
	}
	// A second required producer which has not applied cannot be hidden by the first.
	second := status.Producers[0]
	second.ProducerID = "xray:second"
	second.AppliedRevision = 0
	status.Producers = append(status.Producers, second)
	if err = f.svc.RecordStatus(context.Background(), f.node, status); err == nil {
		t.Fatal("collecting accepted an unapplied required producer")
	}
}
