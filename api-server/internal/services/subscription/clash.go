package subscription

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Proxy group names (Korean, shown in the client UI).
const (
	groupAuto   = "자동 선택"
	groupSelect = "노드 선택"
	groupInfo   = "구독 정보"
)

type clashConfig struct {
	MixedPort               int          `yaml:"mixed-port"`
	AllowLAN                bool         `yaml:"allow-lan"`
	BindAddress             string       `yaml:"bind-address"`
	Mode                    string       `yaml:"mode"`
	LogLevel                string       `yaml:"log-level"`
	IPv6                    bool         `yaml:"ipv6"`
	UnifiedDelay            bool         `yaml:"unified-delay"`
	TCPConcurrent           bool         `yaml:"tcp-concurrent"`
	GlobalClientFingerprint string       `yaml:"global-client-fingerprint"`
	ExternalController      string       `yaml:"external-controller"`
	Profile                 clashProfile `yaml:"profile"`
	Sniffer                 clashSniffer `yaml:"sniffer"`
	DNS                     clashDNS     `yaml:"dns"`
	Proxies                 []any        `yaml:"proxies"`
	ProxyGroups             []proxyGroup `yaml:"proxy-groups"`
	Rules                   []string     `yaml:"rules"`
}

type clashProfile struct {
	StoreSelected bool `yaml:"store-selected"`
	StoreFakeIP   bool `yaml:"store-fake-ip"`
}

type clashSniffer struct {
	Enabled          bool                          `yaml:"enable"`
	OverrideDest     bool                          `yaml:"override-destination"`
	Sniff            map[string]clashSniffPortSpec `yaml:"sniff"`
	SkipDomain       []string                      `yaml:"skip-domain,omitempty"`
	ForceDNSMapping  bool                          `yaml:"force-dns-mapping"`
	ParsePureIP      bool                          `yaml:"parse-pure-ip"`
}

type clashSniffPortSpec struct {
	Ports []string `yaml:"ports"`
}

type clashDNS struct {
	Enable         bool     `yaml:"enable"`
	Listen         string   `yaml:"listen"`
	IPv6           bool     `yaml:"ipv6"`
	EnhancedMode   string   `yaml:"enhanced-mode"`
	FakeIPRange    string   `yaml:"fake-ip-range"`
	FakeIPFilter   []string `yaml:"fake-ip-filter"`
	DefaultServer  []string `yaml:"default-nameserver"`
	Nameserver     []string `yaml:"nameserver"`
	Fallback       []string `yaml:"fallback,omitempty"`
}

type proxyGroup struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Proxies  []string `yaml:"proxies"`
	URL      string   `yaml:"url,omitempty"`
	Interval int      `yaml:"interval,omitempty"`
}

// GenerateClash builds a full-featured Clash/verge config for the user's
// allowed nodes. infoLabels are shown as selectable informational pseudo-nodes
// (e.g. remaining traffic, days until expiry).
func GenerateClash(nodes []NodeInfo, userUUID string, infoLabels []string) ([]byte, error) {
	var proxies []any
	var proxyNames []string

	for _, node := range nodes {
		proxy := clashProxy(node, userUUID)
		if proxy == nil {
			continue
		}
		proxies = append(proxies, proxy)
		proxyNames = append(proxyNames, node.Name)
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no valid proxies generated")
	}

	// Informational pseudo-nodes are real (working) proxies cloned from the
	// first node but renamed, so any Clash client accepts them.
	var infoNames []string
	if template, ok := proxies[0].(map[string]any); ok {
		for _, label := range infoLabels {
			clone := make(map[string]any, len(template))
			for k, v := range template {
				clone[k] = v
			}
			clone["name"] = label
			proxies = append(proxies, clone)
			infoNames = append(infoNames, label)
		}
	}

	selectProxies := make([]string, 0, len(proxyNames)+1)
	selectProxies = append(selectProxies, groupAuto)
	selectProxies = append(selectProxies, proxyNames...)

	groups := []proxyGroup{
		{
			Name:    groupSelect,
			Type:    "select",
			Proxies: selectProxies,
		},
		{
			Name:     groupAuto,
			Type:     "url-test",
			Proxies:  proxyNames,
			URL:      "http://www.gstatic.com/generate_204",
			Interval: 300,
		},
	}
	if len(infoNames) > 0 {
		groups = append(groups, proxyGroup{
			Name:    groupInfo,
			Type:    "select",
			Proxies: infoNames,
		})
	}

	config := clashConfig{
		MixedPort:               7890,
		AllowLAN:                true,
		BindAddress:             "*",
		Mode:                    "rule",
		LogLevel:                "warning",
		IPv6:                    false,
		UnifiedDelay:            true,
		TCPConcurrent:           true,
		GlobalClientFingerprint: "chrome",
		ExternalController:      "127.0.0.1:9090",
		Profile: clashProfile{
			StoreSelected: true,
			StoreFakeIP:   true,
		},
		Sniffer: clashSniffer{
			Enabled:         true,
			OverrideDest:    false,
			ForceDNSMapping: true,
			ParsePureIP:     true,
			Sniff: map[string]clashSniffPortSpec{
				"HTTP": {Ports: []string{"80", "8080-8880"}},
				"TLS":  {Ports: []string{"443", "8443"}},
				"QUIC": {Ports: []string{"443", "8443"}},
			},
			SkipDomain: []string{"+.push.apple.com", "+.apple.com"},
		},
		DNS: clashDNS{
			Enable:        true,
			Listen:        "0.0.0.0:1053",
			IPv6:          false,
			EnhancedMode:  "fake-ip",
			FakeIPRange:   "198.18.0.1/16",
			FakeIPFilter:  []string{"*.lan", "*.local", "*.localhost", "+.pool.ntp.org", "+.ntp.org"},
			DefaultServer: []string{"1.1.1.1", "8.8.8.8"},
			Nameserver:    []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/dns-query"},
		},
		Proxies:     proxies,
		ProxyGroups: groups,
		Rules: []string{
			"DOMAIN-SUFFIX,local,DIRECT",
			"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
			"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
			"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
			"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
			"GEOIP,PRIVATE,DIRECT,no-resolve",
			"MATCH," + groupSelect,
		},
	}

	return yaml.Marshal(config)
}

func clashProxy(node NodeInfo, userUUID string) any {
	switch node.Protocol {
	case "vless_reality":
		return clashVLESSReality(node, userUUID)
	case "vmess_ws":
		return clashVMessWS(node, userUUID)
	case "trojan_tls":
		return clashTrojan(node, userUUID)
	case "shadowsocks":
		return clashShadowsocks(node)
	default:
		return nil
	}
}

func clashVLESSReality(node NodeInfo, uuid string) map[string]any {
	return map[string]any{
		"name":               node.Name,
		"type":               "vless",
		"server":             node.IP,
		"port":               node.Port,
		"uuid":               uuid,
		"network":            "tcp",
		"tls":                true,
		"udp":                true,
		"flow":               "xtls-rprx-vision",
		"client-fingerprint": "chrome",
		"servername":         "www.cloudflare.com",
		"reality-opts": map[string]any{
			"public-key": node.RealityPublicKey,
			"short-id":   node.RealityShortID,
		},
	}
}

func clashVMessWS(node NodeInfo, uuid string) map[string]any {
	wsPath := node.WSPath
	if wsPath == "" {
		wsPath = "/vmess"
	}

	return map[string]any{
		"name":    node.Name,
		"type":    "vmess",
		"server":  node.IP,
		"port":    node.Port,
		"uuid":    uuid,
		"alterId": 0,
		"cipher":  "auto",
		"tls":     true,
		"udp":     true,
		"network": "ws",
		"ws-opts": map[string]any{
			"path": wsPath,
			"headers": map[string]any{
				"Host": node.IP,
			},
		},
	}
}

func clashTrojan(node NodeInfo, uuid string) map[string]any {
	return map[string]any{
		"name":     node.Name,
		"type":     "trojan",
		"server":   node.IP,
		"port":     node.Port,
		"password": uuid,
		"udp":      true,
		"sni":      node.IP,
	}
}

func clashShadowsocks(node NodeInfo) map[string]any {
	method := node.SSMethod
	if method == "" {
		method = "2022-blake3-aes-128-gcm"
	}

	return map[string]any{
		"name":     node.Name,
		"type":     "ss",
		"server":   node.IP,
		"port":     node.Port,
		"cipher":   method,
		"password": node.SSPassword,
		"udp":      true,
	}
}
