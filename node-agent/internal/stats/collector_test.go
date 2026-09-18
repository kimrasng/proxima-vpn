package stats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

// Regression: Xray's counters are read with QueryStats reset=true, which zeroes
// them as a side effect of reading. A sample that failed to POST was simply
// logged and dropped, so the bytes were gone for good - usage silently
// under-reported for the length of any outage, and traffic caps under-counting
// by the same amount.
//
// collect() is exercised directly rather than through the ticker; the xray
// client is nil because these cases only need the accumulate/flush half, which
// is where the loss was.
func TestPendingTrafficSurvivesAFailedSend(t *testing.T) {
	var mu sync.Mutex
	var received [][]client.TrafficStat
	fail := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload client.StatsPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		mu.Lock()
		received = append(received, payload.Traffic)
		shouldFail := fail
		mu.Unlock()

		if shouldFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCollector(nil, client.NewAPIClient(&config.AgentConfig{
		NodeID:    "node-1",
		APIKey:    "key",
		ServerURL: srv.URL,
	}), DefaultInterval, nil)

	// First sample: 100/200 read out of Xray, delivery fails.
	c.accumulate([]xray.TrafficStat{{UUID: "dev-1", Upload: 100, Download: 200}})
	if err := c.flush(context.Background(), nil, nil); err == nil {
		t.Fatal("expected the first send to fail")
	}
	if len(c.pending) != 1 {
		t.Fatalf("pending should still hold the undelivered sample, got %v", c.pending)
	}

	// Second sample: another 50/60. Xray has already zeroed the first, so the
	// only surviving record of it is what the collector kept.
	c.accumulate([]xray.TrafficStat{{UUID: "dev-1", Upload: 50, Download: 60}})

	mu.Lock()
	fail = false
	mu.Unlock()

	if err := c.flush(context.Background(), nil, nil); err != nil {
		t.Fatalf("second send should succeed: %v", err)
	}
	if len(c.pending) != 0 {
		t.Errorf("pending should be cleared after a successful send, got %v", c.pending)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(received))
	}
	last := received[1]
	if len(last) != 1 {
		t.Fatalf("expected one device in the retry, got %d", len(last))
	}
	if last[0].Upload != 150 {
		t.Errorf("upload = %d, want 150 (100 dropped + 50 fresh)", last[0].Upload)
	}
	if last[0].Download != 260 {
		t.Errorf("download = %d, want 260 (200 dropped + 60 fresh)", last[0].Download)
	}
}

func TestPendingClearedAfterSuccessfulSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCollector(nil, client.NewAPIClient(&config.AgentConfig{
		NodeID: "node-1", APIKey: "key", ServerURL: srv.URL,
	}), DefaultInterval, nil)

	c.accumulate([]xray.TrafficStat{{UUID: "dev-1", Upload: 10, Download: 20}})
	if err := c.flush(context.Background(), nil, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(c.pending) != 0 {
		t.Errorf("pending = %v, want empty", c.pending)
	}

	// A tick with no traffic must not re-post the already-delivered sample.
	if err := c.flush(context.Background(), nil, nil); err != nil {
		t.Fatalf("empty flush should be a no-op, got %v", err)
	}
}

func TestAccumulateSumsRepeatedSamplesPerDevice(t *testing.T) {
	c := NewCollector(nil, nil, DefaultInterval, nil)

	c.accumulate([]xray.TrafficStat{
		{UUID: "dev-1", Upload: 1, Download: 2},
		{UUID: "dev-2", Upload: 5, Download: 6},
	})
	c.accumulate([]xray.TrafficStat{
		{UUID: "dev-1", Upload: 10, Download: 20},
	})

	if got := c.pending["dev-1"]; got.Upload != 11 || got.Download != 22 {
		t.Errorf("dev-1 = %+v, want upload 11 download 22", got)
	}
	if got := c.pending["dev-2"]; got.Upload != 5 || got.Download != 6 {
		t.Errorf("dev-2 = %+v, want upload 5 download 6", got)
	}
	if got := c.pending["dev-1"].UUID; got != "dev-1" {
		t.Errorf("uuid not carried through: %q", got)
	}
}
