package subscription

import "testing"

func TestLegacyClashCompatibleDropsMetaOnlyProxies(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "reality", Protocol: "vless_reality"},
		{Name: "vmess", Protocol: "vmess_ws"},
		{Name: "trojan", Protocol: "trojan_tls"},
		{Name: "ss2022", Protocol: "shadowsocks"},
		{Name: "ss-aead", Protocol: "shadowsocks", SSMethod: "aes-256-gcm"},
		{Name: "hy2", Protocol: "hysteria2"},
		{Name: "wg", Protocol: "wireguard"},
	}
	got := LegacyClashCompatible(nodes)
	want := []string{"vmess", "trojan", "ss-aead"}
	if len(got) != len(want) {
		t.Fatalf("got %d proxies, want %v: %+v", len(got), want, got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("proxy %d = %q, want %q", i, got[i].Name, name)
		}
	}
}
