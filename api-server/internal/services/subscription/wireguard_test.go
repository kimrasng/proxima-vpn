package subscription

import (
	"strings"
	"testing"
)

func TestGenerateWireGuardConf(t *testing.T) {
	node := NodeInfo{
		Name:            "tokyo-1 WireGuard",
		IP:              "203.0.113.20",
		Port:            51820,
		Protocol:        "wireguard",
		WGPrivateKey:    "clientPrivKey=",
		WGPeerPublicKey: "serverPubKey=",
		WGAddress:       "10.66.0.2/32",
	}

	out, err := GenerateWireGuardConf(node)
	if err != nil {
		t.Fatalf("GenerateWireGuardConf: %v", err)
	}

	conf := string(out)
	for _, want := range []string{
		"[Interface]",
		"PrivateKey = clientPrivKey=",
		"Address = 10.66.0.2/32",
		"[Peer]",
		"PublicKey = serverPubKey=",
		"Endpoint = 203.0.113.20:51820",
		"AllowedIPs = 0.0.0.0/0, ::/0",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, conf)
		}
	}
}

func TestGenerateWireGuardConf_WrongProtocol(t *testing.T) {
	if _, err := GenerateWireGuardConf(NodeInfo{Protocol: "vless_reality"}); err == nil {
		t.Fatal("expected error for non-wireguard node")
	}
}
