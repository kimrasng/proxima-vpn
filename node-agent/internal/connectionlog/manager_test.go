package connectionlog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

var testNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func uuid(t *testing.T) nodeprov.HistoryUUID {
	t.Helper()
	id, err := nodeprov.NewHistoryUUID()
	must(t, err)
	return id
}

type wireAPI struct {
	mu          sync.Mutex
	cfg         client.ConnectionHistoryConfig
	fail        bool
	invalid     bool
	expired     bool
	bodies      [][]byte
	epochs      map[nodeprov.HistoryUUID]bool
	serverURL   string
	postGate    <-chan struct{}
	postEntered chan<- struct{}
}

func newWireAPI(t *testing.T) (*wireAPI, *client.APIClient) {
	t.Helper()
	epoch := uuid(t)
	wire := &wireAPI{epochs: map[nodeprov.HistoryUUID]bool{epoch: true}, cfg: client.ConnectionHistoryConfig{SchemaVersion: 1, Revision: 1,
		Enabled: true, CollectionEpoch: &epoch, Bindings: []nodeprov.HistoryBinding{}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire.mu.Lock()
		gate, entered := wire.postGate, wire.postEntered
		wire.mu.Unlock()
		if r.Method == http.MethodPost && gate != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		wire.mu.Lock()
		defer wire.mu.Unlock()
		if r.Header.Get("X-Node-Key") != "history-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing node authentication/content type")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/nodes/node-a/connection-history/config" {
			if wire.fail {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if wire.cfg.CollectionEpoch != nil {
				wire.epochs[*wire.cfg.CollectionEpoch] = true
			}
			must(t, json.NewEncoder(w).Encode(wire.cfg))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/nodes/node-a/connection-history" {
			t.Errorf("unexpected HTTP request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, client.ConnectionHistoryMaxBatch+1))
		must(t, err)
		var batch client.ConnectionHistoryBatch
		must(t, json.Unmarshal(body, &batch))
		if len(body) > client.ConnectionHistoryMaxBatch || len(batch.Events) > 1000 || len(batch.Events) == 0 ||
			batch.SchemaVersion != 1 || batch.BatchID.Validate() != nil {
			t.Error("invalid wire batch bounds")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, event := range batch.Events {
			must(t, event.Validate())
			if !wire.epochs[event.CollectionEpoch] {
				t.Error("unissued collection epoch")
			}
		}
		hash, err := nodeprov.HistoryEventsDigest(batch.Events)
		must(t, err)
		if hash != batch.PayloadSHA256 {
			t.Error("incorrect canonical batch hash")
		}
		wire.bodies = append(wire.bodies, append([]byte(nil), body...))
		t.Logf("HTTP %s %s headers=%v body=%s", r.Method, r.URL.Path, r.Header, body)
		if wire.fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		status := "committed"
		if wire.expired {
			status = "expired"
		}
		if wire.invalid {
			hash = "wrong-digest"
		}
		must(t, json.NewEncoder(w).Encode(client.ConnectionHistoryACK{
			BatchID: batch.BatchID, PayloadSHA256: hash, Status: status}))
	}))
	t.Cleanup(server.Close)
	wire.serverURL = server.URL
	return wire, client.NewAPIClient(&config.AgentConfig{ServerURL: server.URL, NodeID: "node-a", APIKey: "history-key"})
}

func observer(t *testing.T, ctx context.Context, id string) (*connectionhistory.Observer, string) {
	t.Helper()
	runtime := id
	if id == "wireguard:wg0" {
		runtime = "wireguard"
	}
	obs, err := connectionhistory.NewObserver(connectionhistory.Options{
		ProducerID: id, Runtime: runtime, RuntimeBuild: id + "-history-test",
		Capabilities: []string{"hot_toggle"}, Clock: func() time.Time { return testNow }})
	must(t, err)
	// macOS Unix socket names have a short sockaddr limit; keep the owned root short.
	dir, err := os.MkdirTemp("", "ha-")
	must(t, err)
	must(t, os.Chmod(dir, 0o700))
	t.Cleanup(func() { must(t, os.RemoveAll(dir)) })
	path := filepath.Join(dir, "s")
	server, err := obs.Listen(ctx, path)
	must(t, err)
	t.Cleanup(func() { must(t, server.Close()) })
	return obs, path
}

func newManager(t *testing.T, api API, dir string) *Manager {
	t.Helper()
	m, err := NewManager(Options{Directory: dir, API: api, Clock: func() time.Time { return testNow }})
	must(t, err)
	return m
}

func startSource(t *testing.T, ctx context.Context, m *Manager, id string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- m.collect(ctx, m.workers[id]) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Logf("source shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("source did not join")
		}
	}
}

func waitStatus(t *testing.T, m *Manager, predicate func(Status) bool) Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		changed := m.Changes()
		status := m.Status()
		if predicate(status) {
			return status
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("status not reached: %+v", m.Status())
		}
	}
}
