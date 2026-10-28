package subscription

import (
	"reflect"
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

	reality := parsed.Proxies[0].(map[string]any)["reality-opts"].(map[string]any)
	if reality["support-x25519mlkem768"] != true {
		t.Errorf("REALITY proxies must enable X25519MLKEM768 for current Xray servers, got %v", reality)
	}

	str := string(out)
	if !strings.Contains(str, "tokyo-1") || !strings.Contains(str, "osaka-1") {
		t.Errorf("expected proxy names in output, got:\n%s", str)
	}
}

// A hex short ID such as "85e12345" reads as a float under YAML 1.2, which
// Mihomo rejects as "invalid REALITY short ID". The generated profile must
// keep it a string.
func TestGenerateClashKeepsNumericLookingShortIDString(t *testing.T) {
	nodes := []NodeInfo{{
		Name: "kt", IP: "203.0.113.10", Port: 443, Protocol: "vless_reality",
		RealityPublicKey: "pubkey", RealityShortID: "85e12345", ServerName: "example.com",
	}}

	out, err := GenerateClash(nodes, "11111111-1111-1111-1111-111111111111", nil)
	if err != nil {
		t.Fatalf("GenerateClash returned error: %v", err)
	}

	var parsed struct {
		Proxies []struct {
			Reality struct {
				ShortID yaml.Node `yaml:"short-id"`
			} `yaml:"reality-opts"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}
	// Mihomo reads the scalar's resolved type, so a plain 85e12345 is a float.
	// Only an explicitly quoted scalar stays a string for every YAML reader.
	node := parsed.Proxies[0].Reality.ShortID
	if node.Value != "85e12345" || node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 {
		t.Errorf("short-id must be a quoted string, got value %q style %v", node.Value, node.Style)
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
		{Name: "future-1", IP: "203.0.113.20", Port: 44300, Protocol: "some-future-protocol"},
	}

	if _, err := GenerateClash(nodes, "uuid", nil); err == nil {
		t.Fatal("expected error when no supported proxies are generated")
	}
}

func TestGenerateClashOmitsUDPOnlyProtocols(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "wg-1", IP: "203.0.113.20", Port: 51820, Protocol: "wireguard",
			WGPrivateKey: "k=", WGPeerPublicKey: "p=", WGAddress: "10.66.0.2/32"},
		{Name: "hy2-1", IP: "203.0.113.20", Port: 44300, Protocol: "hysteria2"},
	}
	if _, err := GenerateClash(nodes, "uuid", nil); err == nil {
		t.Fatal("UDP-only protocols must not produce proxies")
	}
}

func TestGenerateClashTCPOnlyAndInfoFirst(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "vless-1", IP: "203.0.113.10", Port: 443, Protocol: "vless_reality"},
		{Name: "hy2-1", IP: "203.0.113.20", Port: 44300, Protocol: "hysteria2"},
	}
	out, err := GenerateClash(nodes, "uuid", []string{"info-a", "info-b"})
	if err != nil {
		t.Fatalf("GenerateClash: %v", err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(out, &config); err != nil {
		t.Fatalf("unmarshal clash yaml: %v", err)
	}
	proxies := config["proxies"].([]any)
	var names []string
	for _, p := range proxies {
		m := p.(map[string]any)
		names = append(names, m["name"].(string))
		if m["udp"] != false {
			t.Errorf("%v: udp = %v, want false", m["name"], m["udp"])
		}
	}
	if want := []string{"info-a", "info-b", "vless-1"}; !reflect.DeepEqual(names, want) {
		t.Errorf("proxy order = %v, want %v", names, want)
	}
	groups := config["proxy-groups"].([]any)
	if first := groups[0].(map[string]any)["name"]; first != groupInfo {
		t.Errorf("first group = %v, want %v", first, groupInfo)
	}
}
