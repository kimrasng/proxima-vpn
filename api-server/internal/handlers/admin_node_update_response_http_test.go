package handlers

import (
	"context"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestAdminNodeUpdateReturnsSafeFinalEndpointStateThroughHTTP(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dnsStatus string
		wantDNS   map[string]any
	}{
		{
			name: "absent",
			wantDNS: map[string]any{
				"entry_hostname": nil, "entry_dns_status": nil, "entry_dns_error_code": nil,
			},
		},
		{
			name:      "managed error",
			dnsStatus: "error",
			wantDNS: map[string]any{
				"entry_hostname": "entry.example.test", "entry_dns_status": "error", "entry_dns_error_code": "provider_rejected",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := endpointHTTPFixture(t)
			id := seedChainNode(t, f.pool, "update-response-"+crypto.NewUUID(), "exit", 443)
			if tc.dnsStatus != "" {
				seedManagedEndpoint(t, f.pool, id, tc.dnsStatus)
			}
			if _, err := f.pool.Exec(context.Background(), `UPDATE nodes SET reality_private_key='secret-private' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}

			status, body := f.request(fiber.MethodPut, "/admin/nodes/"+id,
				`{"name":"updated","reality_client_sni":"WWW.CLOUDFLARE.COM."}`)
			if status != fiber.StatusOK {
				t.Fatalf("update status=%d: %s", status, body)
			}
			response := endpointObject(t, body)
			for field, expected := range tc.wantDNS {
				actual, present := response[field]
				if !present || actual != expected {
					t.Errorf("%s=%v present=%t, want %v", field, actual, present, expected)
				}
			}
			for field, expected := range map[string]any{
				"reality_client_sni": "www.cloudflare.com", "reality_sni_status": "valid", "reality_sni_error_code": nil,
			} {
				actual, present := response[field]
				if !present || actual != expected {
					t.Errorf("%s=%v present=%t, want %v", field, actual, present, expected)
				}
			}
			if response["id"] != id || response["name"] != "updated" || response["port"] != float64(443) {
				t.Errorf("existing update response fields changed: %v", response)
			}
			for _, field := range []string{
				"owner_node_id", "desired_action", "cleanup_requested_at", "desired_ipv4", "observed_ipv4",
				"provider_id", "provider_config", "provider_error", "raw_provider_error", "reality_private_key",
				"reality_sni_source", "api_key", "reg_token",
			} {
				if _, present := response[field]; present {
					t.Errorf("update response exposed %s", field)
				}
			}
		})
	}
}
