package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestBandwidthPermitSendsAuthenticatedIdentityAndIndependentDirection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes/node/bandwidth/permit" || r.Header.Get("X-Node-Key") != "secret" {
			t.Errorf("wrong authentication or path")
		}
		var req devicebandwidth.PermitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.DeviceUUID != "device" || req.Direction != devicebandwidth.Download || req.Bytes != 32768 {
			t.Errorf("wrong budget request: %+v", req)
		}
		w.Write([]byte(`{"allowed":false,"retry_after_ms":75}`))
	}))
	defer srv.Close()
	c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "node", APIKey: "secret"})
	res, err := c.RequestBandwidthPermit(context.Background(), "device", devicebandwidth.Download, 32768)
	if err != nil || res.Allowed || res.RetryAfterMS != 75 {
		t.Fatalf("permit %+v, %v", res, err)
	}
}

func TestBandwidthPermitErrorsDoNotBecomeUnlimitedAndNeverRedirectCredentials(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte(`{"allowed":true}`)) }))
	defer target.Close()
	for _, code := range []int{302, 403, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(code)
		}))
		c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "node", APIKey: "secret"})
		res, err := c.RequestBandwidthPermit(context.Background(), "device", devicebandwidth.Upload, 100)
		if err == nil || res.Allowed {
			t.Errorf("status %d granted %+v err%v", code, res, err)
		}
		srv.Close()
	}
	if hits != 0 {
		t.Fatal("credential forwarded to redirect")
	}
}

func TestBandwidthPermitRejectsMalformedResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"allowed":false,"retry_after_ms":-1}`, `{"allowed":false,"retry_after_ms":1001}`, `broken`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "node", APIKey: "secret"})
		if res, err := c.RequestBandwidthPermit(context.Background(), "device", devicebandwidth.Upload, 100); err == nil || res.Allowed {
			t.Errorf("malformed response granted %q", body)
		}
		srv.Close()
	}
}
