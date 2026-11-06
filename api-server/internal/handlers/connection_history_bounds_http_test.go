package handlers_test

import (
	"bytes"
	"context"
	"github.com/proximavpn/proxima-vpn/api-server/internal/scheduler"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"testing"
	"time"
)

func TestConnectionHistoryHTTPBoundsGapsAndRetention(t *testing.T) {
	// Given: an enabled epoch whose first event is held past the replay bound.
	f := historyFixture(t)
	f.enable()
	expired := f.event(1, "flow_end", 1, 17, 29)
	expired.EventTime = nodeprov.HistoryTimestamp(f.now.Add(-8 * 24 * time.Hour))
	expiredBatch := f.batch(expired)
	// When: expired replay and a stale producer are uploaded.
	body := f.ingest(expiredBatch, 200)
	// Then: expired is acknowledged without reconstructing history.
	if !bytes.Contains(body, []byte(`"status":"expired"`)) {
		t.Fatalf("expired=%s", body)
	}
	if len(f.page("").Items) != 0 {
		t.Fatal("expired history recreated")
	}
	if _, err := f.db.Exec(context.Background(), `UPDATE connection_log_revisions SET issued_at=now()-interval '9 days' WHERE node_origin=$1`, f.node); err != nil {
		t.Fatal(err)
	}
	f.ingest(f.batch(f.event(2, "flow_end", 2, 41, 59)), 409)
	status := services.ConnectionHistoryStatus{DesiredRevision: 2, AppliedRevision: 2, CollectionEpoch: f.config.CollectionEpoch, Enabled: true, State: "collecting", AppliedAt: historyPtr(nodeprov.HistoryTimestamp(f.now)), Producers: []services.ConnectionHistoryProducerStatus{{ProducerID: "xray", ObserverBootID: &f.boot, RuntimeBuild: "fixture-history-v1", Capabilities: []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"}, AppliedRevision: 2}}}
	if err := f.svc.RecordStatus(context.Background(), f.node, status); err != nil {
		t.Fatal(err)
	}
	update := f.event(2, "flow_update", 2, 41, 59)
	f.ingest(f.batch(update), 200)
	gap := f.event(3, "flow_update", 2, 0, 0)
	gap.Kind = "collection_gap"
	gap.FlowID = nil
	gap.TunnelID = nil
	gap.Protocol = nil
	gap.InboundTag = nil
	gap.Auth = nodeprov.HistoryAuth{Kind: "unknown"}
	gap.Peer = nil
	gap.Destination = nil
	gap.Bytes = nodeprov.HistoryBytes{Layer: "none"}
	gap.Lifecycle = nodeprov.HistoryLifecycle{Scope: "collection"}
	gap.Quality = nodeprov.HistoryQuality{Identity: "unknown", Parent: "unknown", Destination: "unknown", Bytes: "unknown", Start: "unknown", End: "unknown", Completeness: "unknown"}
	gap.Gap = &nodeprov.HistoryGap{ProducerID: "xray", ObserverBootID: f.boot, Reason: "producer_lost"}
	f.ingest(f.batch(gap), 200)
	page := f.page("")
	if len(page.Items) != 1 || page.Items[0].ObservationStatus != "interrupted" || page.Items[0].Event.Lifecycle.EndedAt != nil || len(page.Items[0].GapCoverage) != 1 || page.Items[0].Event.Quality.Completeness != "unknown" {
		t.Fatalf("gap=%+v", page)
	}
	// Retention removes old history but preserves receipts through day 38.
	retention := scheduler.NewConnectionHistoryRetention(f.db)
	if err := retention.RunOnce(context.Background(), time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(f.page("").Items) != 0 {
		t.Fatal("history retained after day30")
	}
	f.ingest(expiredBatch, 200)
	var receipts int
	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM connection_log_batches`).Scan(&receipts); err != nil || receipts != 3 {
		t.Fatalf("38d receipt retained=%d %v", receipts, err)
	}
	if err := retention.RunOnce(context.Background(), time.Now().Add(39*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM connection_log_batches`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("receipt purge=%d %v", receipts, err)
	}
}
