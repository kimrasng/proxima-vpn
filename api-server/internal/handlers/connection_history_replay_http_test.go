package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectionHistoryHTTPReplayConflictReverseAndQuota(t *testing.T) {
	// Given: issued identity and ON epoch, with unrelated quota rows.
	f := historyFixture(t)
	f.enable()
	end := f.event(3, "flow_end", 1, ^uint64(0), 8421)
	start := f.event(1, "flow_start", 1, 0, 0)
	end.Lifecycle.StartedAt = nil
	end.Lifecycle.PartialStart = true
	end.Quality.Start = "unknown"
	batch := f.batch(end)
	// When: end arrives before start, and batches/events replay.
	f.ingest(batch, 200)
	f.ingest(batch, 200)
	f.ingest(f.batch(end), 200)
	f.ingest(f.batch(start), 200)
	// Then: one terminal projection, immutable first observation and numeric U64.
	page := f.page("?limit=1")
	if len(page.Items) != 1 || page.Items[0].Event.Bytes.Upload == nil || uint64(*page.Items[0].Event.Bytes.Upload) != ^uint64(0) || page.Items[0].Event.Lifecycle.StartedAt == nil || page.Items[0].Event.Lifecycle.EndedAt == nil {
		t.Fatalf("projection=%+v", page)
	}
	first := page.Items[0].FirstObservedAt
	detail := f.request("GET", "/api/v1/admin/connection-history/flows/"+f.node+"/"+string(*f.config.CollectionEpoch)+"/"+*end.FlowID, nil, "", 200)
	if !bytes.Contains(detail, []byte(`"upload":"18446744073709551615"`)) {
		t.Fatalf("detail lost numeric bytes: %s", detail)
	}
	f.request("GET", "/api/v1/admin/connection-history/flows/"+f.other+"/"+string(*f.config.CollectionEpoch)+"/"+*end.FlowID, nil, "", 404)
	conflict := end
	conflict.RuntimeBuild = "changed"
	bad := f.batch(conflict)
	bad.BatchID = batch.BatchID
	f.ingest(bad, 409)
	f.ingest(f.batch(conflict), 409)
	regressed := f.event(4, "flow_end", 1, 6, 3)
	f.ingest(f.batch(regressed), 409)
	other := f.event(5, "flow_end", 2, 37, 83)
	f.ingest(f.batch(other), 200)
	page = f.page("?limit=1")
	if page.NextCursor == nil {
		t.Fatal("missing stable cursor")
	}
	f.ingest(f.batch(f.event(6, "flow_end", 3, 143, 257)), 200)
	next := f.page("?limit=1&cursor=" + *page.NextCursor)
	if len(next.Items) != 1 || *next.Items[0].Event.FlowID != *end.FlowID || !next.Items[0].FirstObservedAt.Equal(first) {
		t.Fatalf("cursor moved: %+v", next)
	}
	var quota, traffic, events int64
	err := f.db.QueryRow(context.Background(), `SELECT (SELECT traffic_used FROM users WHERE id=$1),(SELECT sum(up_bytes+dn_bytes) FROM traffic_logs),(SELECT count(*) FROM connection_log_events)`, f.user).Scan(&quota, &traffic, &events)
	if err != nil || quota != 713 || traffic != 228 || events != 4 {
		t.Fatalf("quota=%d traffic=%d events=%d err=%v", quota, traffic, events, err)
	}
	// A different authenticated node cannot use A's epoch or binding.
	f.request("POST", "/api/v1/nodes/"+f.other+"/connection-history", f.batch(end), "history-key-1", 409)
	aggregate := f.request("GET", "/api/v1/admin/connection-history/aggregate", nil, "", 200)
	if !bytes.Contains(aggregate, []byte(`18446744073709551795`)) {
		t.Fatalf("numeric aggregate lost precision: %s", aggregate)
	}
	// A late middle sample must also be monotonic against its predecessor.
	f.ingest(f.batch(f.event(8, "flow_update", 4, 111, 222), f.event(10, "flow_update", 4, 333, 444)), 200)
	f.ingest(f.batch(f.event(9, "flow_update", 4, 99, 211)), 409)
}

func TestConnectionHistoryHTTPAtomicMalformedAndDeletion(t *testing.T) {
	// Given: valid node-issued identity and a terminal snapshot.
	f := historyFixture(t)
	f.enable()
	e := f.event(1, "flow_end", 1, 123, 456)
	wrong := f.event(2, "flow_end", 2, 77, 99)
	wrong.Auth.CredentialRef = historyPtr(historyUUID(t))
	// When: one invalid record accompanies a valid one.
	f.ingest(f.batch(e, wrong), 409)
	// Then: the entire batch, receipts and projections roll back.
	var count int
	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM connection_log_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("atomic ingest=%d %v", count, err)
	}
	batch := f.batch(e)
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	numeric := bytes.Replace(raw, []byte(`"upload":"123"`), []byte(`"upload":123`), 1)
	f.request("POST", "/api/v1/nodes/"+f.node+"/connection-history", numeric, "history-key-0", 400)
	duplicate := bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
	f.request("POST", "/api/v1/nodes/"+f.node+"/connection-history", duplicate, "history-key-0", 400)
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	f.request("POST", "/api/v1/nodes/"+f.node+"/connection-history", pretty.Bytes(), "history-key-0", 200)
	// Retained snapshots survive account/device and node deletion without cascade.
	for _, sql := range []string{`DELETE FROM users WHERE id=$1`, `DELETE FROM nodes WHERE id=$1`} {
		id := f.user
		if strings.Contains(sql, "nodes") {
			id = f.node
		}
		if _, err = f.db.Exec(context.Background(), sql, id); err != nil {
			t.Fatal(err)
		}
	}
	page := f.page("")
	if len(page.Items) != 1 || page.Items[0].UserID != nil || page.Items[0].DeviceID != nil || page.Items[0].UserSnapshot == nil || *page.Items[0].UserSnapshot != "history@example.test" || page.Items[0].NodeName != "history-node-0" {
		t.Fatalf("deleted snapshots=%+v", page)
	}
}
