package client

import (
	"context"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetRevokedDevicesAuthenticatedSnapshotAndFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Node-Key") != "nodekey" || r.URL.Path != "/api/v1/nodes/00000000-0000-0000-0000-000000000001/revoked-devices" {
			t.Errorf("unauthorized request")
		}
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"revoked_uuids":["00000000-0000-0000-0000-000000000002"]}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "00000000-0000-0000-0000-000000000001", APIKey: "nodekey"})
	got, err := c.GetRevokedDevices(context.Background())
	if err != nil || len(got.RevokedUUIDs) != 1 {
		t.Fatalf("snapshot %+v err%v", got, err)
	}
	_, err = c.GetRevokedDevices(context.Background())
	if err == nil {
		t.Fatal("unavailable snapshot accepted")
	}
}

func TestGetRevokedDevicesRejectsMalformedSnapshot(t *testing.T) {
	for _, payload := range []string{`{}`, `{"revoked_uuids":null}`, `{"revoked_uuids":["bad"]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(payload)) }))
		c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "00000000-0000-0000-0000-000000000001", APIKey: "nodekey"})
		if _, err := c.GetRevokedDevices(context.Background()); err == nil {
			t.Errorf("accepted %s", payload)
		}
		srv.Close()
	}
}
