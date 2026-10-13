package main

import (
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"testing"
)

func TestMaterializedSourceCredentialsBindCanonicalAddress(t *testing.T) {
	var credentials []devicebandwidth.Credential
	egress := newDeviceEgress(func(values []devicebandwidth.Credential) error {
		credentials = append([]devicebandwidth.Credential(nil), values...)
		return nil
	})
	// Reuse the live fixture's canonical shape without launching Xray.
	id := "00000000-0000-4000-8000-000000000001"
	tag := devicebandwidth.OutboundPrefix + id + "~0123456789abcdef"
	canonical := []byte(`{"inbounds":[{"tag":"vless-in","protocol":"vless","settings":{"clients":[{"id":"` + id + `","email":"` + id + `@proxima"}]}}],"outbounds":[{"tag":"block","protocol":"blackhole"},{"tag":"` + tag + `","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":10086,"users":[{"user":"` + id + `","pass":"materialize-at-node"}]}]}}],"routing":{"rules":[{"type":"field","user":["` + id + `@proxima"],"source":["203.0.113.2/32"],"outboundTag":"` + tag + `"},{"type":"field","inboundTag":["vless-in"],"outboundTag":"block"}]}}`)
	if _, err := egress.TransformConfig(canonical); err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].UUID != id || credentials[0].SourceAddress != "203.0.113.2" || credentials[0].Password == "materialize-at-node" {
		t.Fatalf("credentials=%+v", credentials)
	}
}
