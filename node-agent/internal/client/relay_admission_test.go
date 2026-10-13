package client

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayAdmissionSendsPeerTupleNotClaimedOriginalAddress(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	for _, status := range []int{204, 409, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Device string `json:"device_uuid"`
				Source string `json:"source_address"`
				Peer   struct {
					IP    string `json:"ip"`
					Port  int    `json:"port"`
					Local int    `json:"local_port"`
				} `json:"relay_peer"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Device != id || body.Source != "" || body.Peer.IP != "10.0.2.1" || body.Peer.Port != 40000 || body.Peer.Local != 8443 {
				t.Errorf("unexpected payload %+v", body)
			}
			if r.Header.Get("X-Node-Key") != "test" {
				t.Error("missing node authentication")
			}
			w.WriteHeader(status)
		}))
		api := NewAPIClient(&config.AgentConfig{ServerURL: server.URL, NodeID: "node", APIKey: "test"})
		err := api.AdmitRelayPeer(context.Background(), id, "10.0.2.1", 40000, 8443)
		if status == 204 && err != nil || status != 204 && err == nil {
			t.Fatalf("status=%d err=%v", status, err)
		}
		if status == 409 && !errors.Is(err, ErrAddressCapacity) {
			t.Fatalf("capacity error=%v", err)
		}
		server.Close()
	}
}
