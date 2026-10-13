package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
)

func TestAdmissionSourceAndLegacyHTTP(t *testing.T) {
	const device = "3a2ecdb6-6283-46c0-a971-e9adeea2a27e"
	for _, source := range []string{"", "192.0.2.1"} {
		for _, status := range []int{204, 409, 503} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/nodes/node/devices/admit" || r.Header.Get("X-Node-Key") != "secret" {
					t.Error("wrong request identity")
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["device_uuid"] != device || body["source_address"] != source {
					t.Errorf("payload=%v", body)
				}
				if source == "" {
					if _, exists := body["source_address"]; exists {
						t.Error("legacy payload changed")
					}
				}
				w.WriteHeader(status)
			}))
			c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "node", APIKey: "secret"})
			var err error
			if source == "" {
				err = c.AdmitDevice(context.Background(), device)
			} else {
				err = c.AdmitSource(context.Background(), device, source)
			}
			if status == 204 && err != nil || status != 204 && err == nil {
				t.Errorf("status=%d err=%v", status, err)
			}
			if status == 409 {
				want := ErrUUIDCapacity
				if source != "" {
					want = ErrAddressCapacity
				}
				if !errors.Is(err, want) {
					t.Errorf("err=%v want=%v", err, want)
				}
			}
			srv.Close()
		}
	}
}

func TestAdmissionSourceRejectsRedirectAndInvalidIdentity(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(204) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(307)
	}))
	defer srv.Close()
	c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "node", APIKey: "secret"})
	for _, source := range []string{"192.0.2.1", ""} {
		if err := c.AdmitSource(context.Background(), "3a2ecdb6-6283-46c0-a971-e9adeea2a27e", source); err == nil {
			t.Fatal("admission unexpectedly succeeded")
		}
	}
	if err := c.AdmitSource(context.Background(), "bad", "192.0.2.1"); err == nil {
		t.Fatal("invalid UUID accepted")
	}
	if hits != 0 {
		t.Fatal("redirect forwarded credentials")
	}
}
