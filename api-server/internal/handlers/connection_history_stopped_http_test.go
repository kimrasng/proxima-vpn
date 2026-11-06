package handlers_test

import (
	"encoding/json"
	"net/url"
	"slices"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestConnectionHistoryHTTPCollectionStopped(t *testing.T) {
	for _, order := range []string{"detail_before_boundary", "boundary_before_detail"} {
		t.Run(order, func(t *testing.T) {
			// Given: live session/flow samples and separate exact terminal owners.
			f := historyFixture(t)
			f.enable()
			epoch := *f.config.CollectionEpoch
			session := func(sequence uint64, kind, id string) nodeprov.HistoryEvent {
				e := f.event(sequence, "flow_update", 1, 0, 0)
				e.Kind, e.TunnelID = kind, historyPtr(string(f.boot)+":t:"+id)
				e.FlowID, e.Destination = nil, nil
				e.Bytes = nodeprov.HistoryBytes{Layer: "none"}
				e.Lifecycle.Scope = "authenticated_transport"
				e.Quality.Bytes, e.Quality.Destination = "unknown", "unknown"
				if kind == "tunnel_end" {
					e.Lifecycle.EndedAt = e.Lifecycle.LastObservedAt
					e.Lifecycle.EndReason = historyPtr("eof")
					e.Lifecycle.EndSemantics = historyPtr("local_owner_closed")
					e.Quality.End = "exact"
				}
				return e
			}
			start := f.event(2, "flow_start", 1, 0, 0)
			update := f.event(4, "flow_update", 1, 29, 31)
			ended := f.event(5, "flow_end", 2, 131, 271)
			ended.TunnelID = historyPtr(string(f.boot) + ":t:2")
			details := []nodeprov.HistoryEvent{
				session(1, "tunnel_start", "1"), start,
				session(3, "tunnel_update", "1"), update, ended,
				session(6, "tunnel_end", "2"),
			}
			// A different boot in the same epoch has not crossed this barrier.
			other := f.event(2, "flow_update", 3, 17, 19)
			other.ObserverBootID = historyUUID(t)
			other.EventID = string(other.ObserverBootID) + ":2"
			other.FlowID = historyPtr(string(other.ObserverBootID) + ":f:3")
			other.TunnelID = historyPtr(string(other.ObserverBootID) + ":t:1")
			f.ingest(f.batch(other), 200)
			toggle := func(enabled bool, revision nodeprov.HistoryU64) {
				f.request("POST", "/api/v1/admin/nodes/connection-logging", services.ConnectionHistoryBulkCommand{
					Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: revision}}, Enabled: enabled,
				}, "", 200)
			}
			toggle(false, 2)
			boundary := nodeprov.HistoryEvent{
				SchemaVersion: 1, EventID: string(f.boot) + ":10", ObserverBootID: f.boot, Sequence: 10,
				ProducerID: "xray", Runtime: "xray", RuntimeBuild: "fixture-history-v1",
				ConfigRevision: 3, CollectionEpoch: epoch, EventTime: nodeprov.HistoryTimestamp(f.now), MonotonicNS: 1000,
				Kind: "collection_boundary", Auth: nodeprov.HistoryAuth{Kind: "unknown"},
				Bytes: nodeprov.HistoryBytes{Layer: "none"}, Lifecycle: nodeprov.HistoryLifecycle{Scope: "collection"},
				Quality:  nodeprov.HistoryQuality{Identity: "unknown", Parent: "unknown", Destination: "unknown", Bytes: "unknown", Start: "unknown", End: "unknown", Completeness: "complete"},
				Boundary: &nodeprov.HistoryBoundary{Action: "disabled", PriorRevision: 2},
			}
			page := func(resource, query string) services.ConnectionHistoryPage {
				t.Helper()
				var result services.ConnectionHistoryPage
				raw := f.request("GET", "/api/v1/admin/connection-history/"+resource+"?collection_epoch="+string(epoch)+query, nil, "", 200)
				if err := json.Unmarshal(raw, &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			// When: the applied barrier and retained details arrive independently.
			if order == "boundary_before_detail" {
				f.ingest(f.batch(boundary), 200)
				slices.Reverse(details)
				f.ingest(f.batch(details...), 200)
			} else {
				f.ingest(f.batch(details...), 200)
				if rows := page("flows", "&status=observing"); len(rows.Items) != 2 {
					t.Fatalf("desired OFF alone stopped collection: %+v", rows)
				}
				f.ingest(f.batch(boundary), 200)
			}
			// Then: both list and encoded detail distinguish collection stop from VPN end.
			for _, resource := range []string{"flows", "sessions"} {
				for _, row := range page(resource, "").Items {
					if row.Event.ObserverBootID == f.boot && row.Event.Lifecycle.EndedAt == nil && row.ObservationStatus != "collection_stopped" {
						t.Fatalf("%s retained disabled interval status=%s want=collection_stopped", resource, row.ObservationStatus)
					}
				}
				rows := page(resource, "&status=collection_stopped")
				if len(rows.Items) != 1 {
					t.Fatalf("%s stopped rows=%+v", resource, rows)
				}
				row := rows.Items[0]
				id := *row.Event.TunnelID
				if resource == "flows" {
					id = *row.Event.FlowID
					if *row.Event.Bytes.Upload != 29 || *row.Event.Bytes.Download != 31 {
						t.Fatalf("stopped counters=%+v", row.Event.Bytes)
					}
				}
				if row.ObservationStatus != "collection_stopped" || row.Event.Lifecycle.EndedAt != nil ||
					row.Event.Lifecycle.EndReason != nil || row.Event.Lifecycle.EndSemantics != nil ||
					row.Event.Bytes.Final || row.Event.Quality.End != "unknown" || row.Event.Quality.Completeness != "partial" ||
					*row.Event.Lifecycle.LastObservedAt != nodeprov.HistoryTimestamp(f.now) || len(row.GapCoverage) != 0 {
					t.Fatalf("%s fabricated stop evidence: %+v", resource, row)
				}
				var detail services.ConnectionHistoryRow
				raw := f.request("GET", "/api/v1/admin/connection-history/"+resource+"/"+f.node+"/"+string(epoch)+"/"+url.PathEscape(id), nil, "", 200)
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatal(err)
				}
				if detail.ObservationStatus != row.ObservationStatus || detail.Event.Lifecycle.EndedAt != nil || detail.Event.Bytes.Final {
					t.Fatalf("detail disagrees with stopped list: %+v", detail)
				}
				terminal := page(resource, "&status=ended")
				if len(terminal.Items) != 1 || terminal.Items[0].Event.Lifecycle.EndedAt == nil ||
					terminal.Items[0].Event.Quality.End != "exact" || *terminal.Items[0].Event.Lifecycle.EndReason != "eof" {
					t.Fatalf("terminal evidence changed: %+v", terminal)
				}
			}
			if rows := page("flows", "&status=observing"); len(rows.Items) != 1 || *rows.Items[0].Event.FlowID != *other.FlowID {
				t.Fatalf("unrelated boot stopped: %+v", rows)
			}
			aggregate := func(query string, complete, partial int64, upload string) {
				t.Helper()
				var totals []services.ConnectionHistoryLayerTotal
				raw := f.request("GET", "/api/v1/admin/connection-history/aggregate?collection_epoch="+string(epoch)+query, nil, "", 200)
				if err := json.Unmarshal(raw, &totals); err != nil {
					t.Fatal(err)
				}
				if len(totals) != 1 || totals[0].CompleteIntervals != complete || totals[0].PartialIntervals != partial || totals[0].Upload != upload {
					t.Fatalf("aggregate=%s", raw)
				}
			}
			aggregate("", 1, 2, "131")
			aggregate("&status=collection_stopped", 0, 1, "0")
			aggregate("&status=ended", 1, 0, "131")
			// Late samples and a new epoch cannot reopen the old stopped interval.
			f.ingest(f.batch(start), 200)
			f.ingest(f.batch(f.event(7, "flow_update", 1, 37, 41)), 200)
			f.ingest(f.batch(f.event(11, "flow_update", 1, 90, 90)), 409)
			toggle(true, 3)
			raw := f.request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, "history-key-0", 200)
			if err := json.Unmarshal(raw, &f.config); err != nil {
				t.Fatal(err)
			}
			f.ingest(f.batch(f.event(12, "flow_start", 1, 0, 0)), 200)
			if rows := page("flows", "&status=collection_stopped"); len(rows.Items) != 1 || *rows.Items[0].Event.Bytes.Upload != 37 {
				t.Fatalf("old epoch reopened: %+v", rows)
			}
			if rows := f.page("?collection_epoch=" + string(*f.config.CollectionEpoch) + "&status=observing"); len(rows.Items) != 1 {
				t.Fatalf("new epoch inherited stop: %+v", rows)
			}
			// Gap uncertainty is retained independently of the known stop.
			gap := boundary
			gap.Kind, gap.EventID, gap.Sequence, gap.ConfigRevision = "collection_gap", string(f.boot)+":8", 8, 2
			gap.Boundary = nil
			gap.Gap = &nodeprov.HistoryGap{ProducerID: "xray", ObserverBootID: f.boot, Reason: "producer_lost"}
			gap.Quality.Completeness = "unknown"
			f.ingest(f.batch(gap), 200)
			for _, resource := range []string{"flows", "sessions"} {
				rows := page(resource, "&status=collection_stopped")
				if len(rows.Items) != 1 || rows.Items[0].Event.Quality.Completeness != "unknown" ||
					len(rows.Items[0].GapCoverage) != 1 || rows.Items[0].Event.Bytes.Final || rows.Items[0].Event.Lifecycle.EndedAt != nil {
					t.Fatalf("gap lost or stopped state hidden: %+v", rows)
				}
				if rows := page(resource, "&status=interrupted"); len(rows.Items) != 0 {
					t.Fatalf("status filter disagrees with stop: %+v", rows)
				}
				terminal := page(resource, "&status=ended")
				if len(terminal.Items) != 1 || terminal.Items[0].Event.Lifecycle.EndedAt == nil || terminal.Items[0].Event.Quality.End != "exact" ||
					terminal.Items[0].Event.Quality.Completeness != "unknown" || terminal.Items[0].Event.Bytes.Final {
					t.Fatalf("gap changed exact terminal end: %+v", terminal)
				}
			}
			aggregate("&status=collection_stopped", 0, 1, "0")
		})
	}
}
