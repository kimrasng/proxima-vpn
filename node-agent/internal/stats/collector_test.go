package stats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

func collectorForTest(t *testing.T, dir, url string) *Collector {
	t.Helper()
	c, err := NewCollector(nil, client.NewAPIClient(&config.AgentConfig{
		NodeID: "node-1", APIKey: "key", ServerURL: url,
	}), DefaultInterval, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOutboxRetriesStableBatchAndReplaysAfterRestart(t *testing.T) {
	dir := t.TempDir()
	var received []client.StatsPayload
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p client.StatsPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode: %v", err)
		}
		received = append(received, p)
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	c := collectorForTest(t, dir, srv.URL)
	c.accumulate([]xray.TrafficStat{{UUID: "dev-1", Upload: 100, Download: 200}})
	if err := c.queuePending(); err != nil {
		t.Fatal(err)
	}
	if len(c.pending) != 0 || len(c.outbox.files) != 1 {
		t.Fatalf("sample not durably queued: %+v", c)
	}
	if err := c.drain(context.Background()); err == nil {
		t.Fatal("expected failed send")
	}

	// A fresh sample must be a separate batch, never mixed into an
	// ambiguously delivered batch's payload or ID.
	c.accumulate([]xray.TrafficStat{{UUID: "dev-1", Upload: 50, Download: 60}})
	if err := c.queuePending(); err != nil {
		t.Fatal(err)
	}
	if len(c.outbox.files) != 2 {
		t.Fatalf("outbox: %v", c.outbox.files)
	}

	c = collectorForTest(t, dir, srv.URL) // process restart
	fail = false
	if err := c.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.outbox.files) != 0 {
		t.Fatalf("not drained: %v", c.outbox.files)
	}
	if len(received) != 3 {
		t.Fatalf("received %d requests", len(received))
	}
	if received[0].BatchID == "" || received[0].BatchID != received[1].BatchID ||
		!reflect.DeepEqual(received[0].Traffic, received[1].Traffic) {
		t.Errorf("ambiguous batch changed on retry: %+v", received)
	}
	if received[2].BatchID == received[1].BatchID || received[2].Traffic[0].Upload != 50 {
		t.Errorf("new sample mixed with old: %+v", received)
	}
}

func TestOnlineOnlyReportDoesNotReplay(t *testing.T) {
	var received []client.StatsPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p client.StatsPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode: %v", err)
		}
		received = append(received, p)
	}))
	defer srv.Close()
	c := collectorForTest(t, t.TempDir(), srv.URL)
	if err := c.apiClient.SendStats(context.Background(), "", nil, []string{"online"}, nil); err != nil {
		t.Fatal(err)
	}
	c = collectorForTest(t, c.outbox.dir, srv.URL)
	if err := c.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].BatchID != "" || len(received[0].OnlineUUIDs) != 1 {
		t.Errorf("online report replayed or batched: %+v", received)
	}
}

func TestOutboxRejectsCorruptFileAndEnforcesBound(t *testing.T) {
	dir := t.TempDir()
	c := collectorForTest(t, dir, "http://localhost")
	b, err := newBatch([]client.TrafficStat{{UUID: "dev-1", Upload: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.outbox.append(b); err != nil {
		t.Fatal(err)
	}
	name := c.outbox.files[0]
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openOutbox(dir); err == nil {
		t.Fatal("corrupt outbox silently accepted")
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	c = collectorForTest(t, dir, "http://localhost")
	c.outbox.bytes = maxOutboxBytes - maxBatchBytes + 1
	if c.hasCapacity() {
		t.Fatal("expected pre-poll capacity limit")
	}
	c.outbox.bytes = maxOutboxBytes
	if err := c.outbox.append(b); err == nil {
		t.Fatal("expected append capacity limit")
	}
}

func TestAccumulateSumsRepeatedSamplesPerDevice(t *testing.T) {
	c := collectorForTest(t, t.TempDir(), "http://localhost")
	c.accumulate([]xray.TrafficStat{{UUID: "dev-1", Upload: 1, Download: 2}, {UUID: "dev-2", Upload: 5, Download: 6}})
	c.accumulate([]xray.TrafficStat{{UUID: "dev-1", Upload: 10, Download: 20}})
	if got := c.pending["dev-1"]; got.Upload != 11 || got.Download != 22 || got.UUID != "dev-1" {
		t.Errorf("dev-1 = %+v", got)
	}
	if got := c.pending["dev-2"]; got.Upload != 5 || got.Download != 6 {
		t.Errorf("dev-2 = %+v", got)
	}
}
