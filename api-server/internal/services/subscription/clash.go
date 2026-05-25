package subscription

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type clashConfig struct {
	Port        int          `yaml:"port"`
	SocksPort   int          `yaml:"socks-port"`
	AllowLAN    bool         `yaml:"allow-lan"`
	Mode        string       `yaml:"mode"`
	Proxies     []any        `yaml:"proxies"`
	ProxyGroups []proxyGroup `yaml:"proxy-groups"`
	Rules       []string     `yaml:"rules"`
}

type proxyGroup struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Proxies  []string `yaml:"proxies"`
	URL      string   `yaml:"url,omitempty"`
	Interval int      `yaml:"interval,omitempty"`
}

func GenerateClash(nodes []NodeInfo, userUUID string) ([]byte, error) {
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

	selectProxies := make([]string, 0, len(proxyNames)+1)
	selectProxies = append(selectProxies, "Auto")
	selectProxies = append(selectProxies, proxyNames...)

	config := clashConfig{
		Port:      7890,
		SocksPort: 7891,
		AllowLAN:  false,
		Mode:      "rule",
		Proxies:   proxies,
		ProxyGroups: []proxyGroup{
			{
				Name:     "Auto",
				Type:     "url-test",
				Proxies:  proxyNames,
				URL:      "http://www.gstatic.com/generate_204",
				Interval: 300,
			},
			{
				Name:    "Select",
				Type:    "select",
				Proxies: selectProxies,
			},
		},
		Rules: []string{
			"MATCH,Select",
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
		"servername":         "www.microsoft.com",
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
