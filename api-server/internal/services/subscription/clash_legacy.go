package subscription

import "strings"

// LegacyClashCompatible keeps only proxies the original (non-Meta) Clash core
// and Stash can parse. Original Clash has no VLESS, Hysteria2 or WireGuard and
// no Shadowsocks 2022 ciphers; one unknown proxy makes it reject the whole
// profile, so they are dropped instead of emitted.
func LegacyClashCompatible(nodes []NodeInfo) []NodeInfo {
	out := make([]NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		switch node.Protocol {
		case "vmess_ws", "trojan_tls":
			out = append(out, node)
		case "shadowsocks":
			method := node.SSMethod
			if method == "" {
				method = "2022-blake3-aes-128-gcm" // clashShadowsocks default
			}
			if !strings.HasPrefix(method, "2022-") {
				out = append(out, node)
			}
		}
	}
	return out
}
