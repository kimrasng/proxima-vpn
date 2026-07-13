package subscription

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateClashValidProxies(t *testing.T) {
	nodes := []NodeInfo{
		{
			Name:             "tokyo-1",
			IP:               "203.0.113.10",
			Port:             443,
			Protocol:         "vless_reality",
			RealityPublicKey: "pubkey",
			RealityShortID:   "abcd",
		},
		{
			Name:       "osaka-1",
			IP:         "203.0.113.11",
			Port:       8443,
			Protocol:   "shadowsocks",
			SSMethod:   "2022-blake3-aes-128-gcm",
			SSPassword: "secret",
		},
	}

	out, err := GenerateClash(nodes, "11111111-1111-1111-1111-111111111111", nil)
	if err != nil {
		t.Fatalf("GenerateClash returned error: %v", err)
	}

	var parsed clashConfig
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}

	if len(parsed.Proxies) != 2 {
		t.Fatalf("expected 2 proxies, got %d", len(parsed.Proxies))
	}

	str := string(out)
	if !strings.Contains(str, "tokyo-1") || !strings.Contains(str, "osaka-1") {
		t.Errorf("expected proxy names in output, got:\n%s", str)
	}
}

func TestGenerateClashInfoNodes(t *testing.T) {
	nodes := []NodeInfo{
		{
			Name:             "tokyo-1",
			IP:               "203.0.113.10",
			Port:             443,
			Protocol:         "vless_reality",
			RealityPublicKey: "pubkey",
			RealityShortID:   "abcd",
		},
	}

	labels := []string{"잔여 트래픽: 70.0 GB", "만료까지: 30일"}
	out, err := GenerateClash(nodes, "uuid", labels)
	if err != nil {
		t.Fatalf("GenerateClash returned error: %v", err)
	}

	var parsed clashConfig
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}

	// 1 real node + 2 info pseudo-nodes.
	if len(parsed.Proxies) != 3 {
		t.Fatalf("expected 3 proxies (1 node + 2 info), got %d", len(parsed.Proxies))
	}

	str := string(out)
	for _, label := range labels {
		if !strings.Contains(str, label) {
			t.Errorf("expected info label %q in output", label)
		}
	}
}

func TestGenerateClashSkipsUnsupportedProtocols(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "wg-1", IP: "203.0.113.20", Port: 51820, Protocol: "wireguard"},
	}

	if _, err := GenerateClash(nodes, "uuid", nil); err == nil {
		t.Fatal("expected error when no supported proxies are generated")
	}
}
