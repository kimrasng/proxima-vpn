package client

import (
	"context"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBandwidthPermitConcurrencyLimitIsTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"allowed":false,"retry_after_ms":0,"denied_reason":"concurrency_limit"}`))
	}))
	defer srv.Close()
	c := NewAPIClient(&config.AgentConfig{ServerURL: srv.URL, NodeID: "node", APIKey: "key"})
	permit, err := c.RequestBandwidthPermit(context.Background(), "device", devicebandwidth.Upload, 100)
	if err != nil || permit.Allowed || permit.DeniedReason != "concurrency_limit" {
		t.Fatalf("terminal permit %+v err%v", permit, err)
	}
}
