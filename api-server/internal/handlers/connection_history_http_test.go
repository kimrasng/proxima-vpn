package handlers_test

import (
	"bytes"
	"context"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"net/http/httptest"
	"testing"
)

func TestConnectionHistoryHTTPControlAtomicityAndIsolation(t *testing.T) {
	// Given: two independently authenticated nodes, only A has a fresh supplier.
	f := historyFixture(t)
	f.status(f.node)
	initial := f.request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, "history-key-0", 200)
	if !bytes.Contains(initial, []byte(`"enabled":false`)) || !bytes.Contains(initial, []byte(`"revision":"0"`)) || !bytes.Contains(initial, []byte(`"collection_epoch":null`)) {
		t.Fatalf("not default OFF: %s", initial)
	}
	// When: mixed capability/revision commands are sent through real auth.
	cmd := services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node)}, {ID: nodeprov.HistoryUUID(f.other)}}, Enabled: true}
	f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
	// Then: no desired policy changed, including the otherwise valid node.
	var changed int
	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM connection_log_controls WHERE enabled OR revision<>0`).Scan(&changed); err != nil || changed != 0 {
		t.Fatalf("atomicity changed=%d err=%v", changed, err)
	}
	f.status(f.other)
	cmd.Nodes[1].ExpectedRevision = 9
	f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
	cmd.Nodes[1].ExpectedRevision = 0
	f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 200)
	cmd.Nodes[0].ExpectedRevision = 1
	cmd.Nodes[1].ExpectedRevision = 1
	f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 200)
	for _, node := range []string{f.node, f.other} {
		var rev string
		if err := f.db.QueryRow(context.Background(), `SELECT revision::text FROM connection_log_controls WHERE node_origin=$1`, node).Scan(&rev); err != nil || rev != "1" {
			t.Fatalf("no-op bumped %s %v", rev, err)
		}
	}
	f.request("GET", "/api/v1/nodes/"+f.other+"/connection-history/config", nil, "history-key-0", 401)
	req := httptest.NewRequest("GET", "/api/v1/admin/connection-history/flows", nil)
	resp, err := f.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal(resp.StatusCode)
	}
}

func TestConnectionHistoryHTTPCapabilityAndRoleValidation(t *testing.T) {
	// Given: A has a fresh observer; B is an entry-only node.
	f := historyFixture(t)
	f.status(f.node)
	f.status(f.other)
	cmd := services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node)}, {ID: nodeprov.HistoryUUID(f.other)}}, Enabled: true}
	if _, err := f.db.Exec(context.Background(), `UPDATE nodes SET role='relay' WHERE id=$1`, f.other); err != nil {
		t.Fatal(err)
	}
	// When: a mixed-role command requests enable.
	body := f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
	// Then: capability and role validation are atomic and have per-node reasons.
	if !bytes.Contains(body, []byte("entry_only")) {
		t.Fatalf("missing role reason: %s", body)
	}
	if _, err := f.db.Exec(context.Background(), `UPDATE nodes SET role='exit' WHERE id=$1`, f.other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(context.Background(), `UPDATE connection_log_controls SET status_at=now()-interval '1 minute' WHERE node_origin=$1`, f.other); err != nil {
		t.Fatal(err)
	}
	body = f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
	if !bytes.Contains(body, []byte("capabilities_stale")) {
		t.Fatalf("missing stale reason: %s", body)
	}
	f.status(f.other)
	if _, err := f.db.Exec(context.Background(), `UPDATE inbounds SET protocol='wireguard' WHERE node_id=$1`, f.other); err != nil {
		t.Fatal(err)
	}
	body = f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
	if !bytes.Contains(body, []byte("unsupported:wireguard")) {
		t.Fatalf("static support claimed: %s", body)
	}
	cmd.Nodes[1].ID = historyUUID(t)
	body = f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 409)
	if !bytes.Contains(body, []byte("node_not_found")) {
		t.Fatalf("missing node reason: %s", body)
	}
	var count int
	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM connection_log_controls WHERE enabled OR revision<>0`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bulk not atomic: %d %v", count, err)
	}
}

func TestConnectionHistoryHTTPOldEpochBoundariesAndBindingRotation(t *testing.T) {
	// Given: ON policy with a live observation, followed by desired OFF.
	f := historyFixture(t)
	f.enable()
	start := f.event(2, "flow_start", 1, 0, 0)
	cmd := services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: 2}}, Enabled: false}
	f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 200)
	boundary := f.event(5, "flow_update", 1, 0, 0)
	boundary.Kind = "collection_boundary"
	boundary.ConfigRevision = 3
	boundary.FlowID = nil
	boundary.TunnelID = nil
	boundary.Protocol = nil
	boundary.InboundTag = nil
	boundary.Auth = nodeprov.HistoryAuth{Kind: "unknown"}
	boundary.Peer = nil
	boundary.Destination = nil
	boundary.Bytes = nodeprov.HistoryBytes{Layer: "none"}
	boundary.Lifecycle = nodeprov.HistoryLifecycle{Scope: "collection"}
	boundary.Quality = nodeprov.HistoryQuality{Identity: "unknown", Parent: "unknown", Destination: "unknown", Bytes: "unknown", Start: "unknown", End: "unknown", Completeness: "complete"}
	boundary.Boundary = &nodeprov.HistoryBoundary{Action: "disabled", PriorRevision: 2}
	// When: disabling boundary arrives before the queued start/update.
	f.ingest(f.batch(boundary), 200)
	f.ingest(f.batch(start, f.event(4, "flow_update", 1, 101, 203)), 200)
	// Then: old queued data survives OFF, while post-barrier detail is rejected.
	f.ingest(f.batch(f.event(6, "flow_end", 1, 109, 211)), 409)
	page := f.page("")
	if len(page.Items) != 1 || page.Items[0].Event.Bytes.Final || page.Items[0].Event.Lifecycle.EndedAt != nil {
		t.Fatalf("disable fabricated end: %+v", page)
	}
	// A binding rotation revokes an immutable generation without reassigning it.
	if err := f.svc.RevokeBinding(context.Background(), f.node, f.binding.CredentialRef, 3); err != nil {
		t.Fatal(err)
	}
	older := f.event(3, "flow_update", 1, 71, 89)
	f.ingest(f.batch(older), 200)
	cfg, err := f.svc.Config(context.Background(), f.node)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bindings) != 0 || cfg.Revision != 4 {
		t.Fatalf("revoked manifest=%+v", cfg)
	}
	// New enable always creates a distinct epoch and zero-baseline projection.
	oldEpoch := *f.config.CollectionEpoch
	cmd.Enabled = true
	cmd.Nodes[0].ExpectedRevision = 4
	f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", 200)
	cfg, err = f.svc.Config(context.Background(), f.node)
	if err != nil {
		t.Fatal(err)
	}
	f.config = cfg
	if *cfg.CollectionEpoch == oldEpoch {
		t.Fatal("epoch reused")
	}
	newStart := f.event(7, "flow_start", 1, 0, 0)
	f.ingest(f.batch(newStart), 200)
	if len(f.page("").Items) != 2 {
		t.Fatal("epoch projections merged")
	}
	admin := f.request("GET", "/api/v1/admin/nodes/"+f.node+"/connection-logging", nil, "", 200)
	if bytes.Contains(admin, []byte("private-configured-label")) || bytes.Contains(admin, []byte("credential_ref")) {
		t.Fatalf("private manifest leaked: %s", admin)
	}
}
