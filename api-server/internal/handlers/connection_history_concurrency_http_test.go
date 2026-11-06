package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestConnectionHistoryHTTPConcurrentCASAndReplay(t *testing.T) {
	// Given: one node at revision zero and two simultaneous admin commands.
	f := historyFixture(t)
	f.status(f.node)
	cmd := services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node)}}, Enabled: true}
	raw, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("HTTP CONCURRENT REQUEST 2x POST /api/v1/admin/nodes/connection-logging body=%s DB before=%s", raw, f.dbState())
	run := func(path, key string, payload []byte) (int, []byte, error) {
		req := httptest.NewRequest("POST", path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("X-Node-Key", key)
		} else {
			req.Header.Set("Authorization", "Bearer "+f.token)
		}
		resp, e := f.app.Test(req, 15000)
		if e != nil {
			return 0, nil, e
		}
		body, e := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if e == nil {
			e = closeErr
		}
		t.Logf("HTTP CONCURRENT RESPONSE status=%d headers=%v body=%s", resp.StatusCode, resp.Header, body)
		return resp.StatusCode, body, e
	}
	results := make(chan int, 2)
	failures := make(chan error, 2)
	gate := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-gate
			status, _, e := run("/api/v1/admin/nodes/connection-logging", "", raw)
			if e != nil {
				failures <- e
			}
			results <- status
		}()
	}
	ready.Wait()
	// When: both requests are released after their subscriptions are installed.
	close(gate)
	a, b := <-results, <-results
	// Then: exactly one compare-and-swap commits.
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatalf("CAS statuses=%d,%d", a, b)
	}
	select {
	case e := <-failures:
		t.Fatal(e)
	default:
	}
	cfg, err := f.svc.Config(context.Background(), f.node)
	if err != nil {
		t.Fatal(err)
	}
	f.config = cfg
	// Both uploads of the same unknown-identity snapshot commit one event.
	e := f.event(1, "flow_end", 1, 43, 79)
	e.Auth = nodeprov.HistoryAuth{Kind: "unknown"}
	e.Quality.Identity = "unknown"
	batch := f.batch(e)
	raw, err = json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("HTTP CONCURRENT REQUEST 2x POST /api/v1/nodes/%s/connection-history body=%s DB before=%s", f.node, raw, f.dbState())
	gate = make(chan struct{})
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-gate
			status, _, e := run("/api/v1/nodes/"+f.node+"/connection-history", "history-key-0", raw)
			if e != nil {
				failures <- e
			}
			results <- status
		}()
	}
	ready.Wait()
	close(gate)
	a, b = <-results, <-results
	if a != 200 || b != 200 {
		t.Fatalf("replay statuses=%d,%d", a, b)
	}
	select {
	case e := <-failures:
		t.Fatal(e)
	default:
	}
	var events, batches int
	if err = f.db.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM connection_log_events),(SELECT count(*) FROM connection_log_batches)`).Scan(&events, &batches); err != nil || events != 1 || batches != 1 {
		t.Fatalf("replay events=%d batches=%d err=%v", events, batches, err)
	}
	t.Logf("HTTP CONCURRENT DB after=%s", f.dbState())
}
