package subscription

import (
	"fmt"
	"strings"
)

func GenerateQuantumult(nodes []NodeInfo, userUUID string) ([]byte, error) {
	var lines []string

	for _, node := range nodes {
		if node.Protocol == "vless_reality" {
			continue
		}

		line := buildQuantumultLine(node, userUUID)
		if line == "" {
			continue
		}

		lines = append(lines, line)
	}

	return []byte(strings.Join(lines, "\n")), nil
}

func buildQuantumultLine(node NodeInfo, userUUID string) string {
	switch node.Protocol {
	case "vmess":
		parts := []string{
			fmt.Sprintf("vmess=%s:%d", node.IP, node.Port),
			"method=chacha20-poly1305",
			fmt.Sprintf("password=%s", userUUID),
		}
		if node.WSPath != "" {
			parts = append(parts, "obfs=ws", fmt.Sprintf("obfs-host=%s", node.IP), fmt.Sprintf("obfs-uri=%s", node.WSPath))
		}
		if node.TLSEnabled {
			parts = append(parts, "over-tls=true", "tls-verification=false")
		}
		parts = append(parts, fmt.Sprintf("tag=%s", node.Name))
		return strings.Join(parts, ", ")

	case "trojan":
		return fmt.Sprintf("trojan=%s:%d, password=%s, over-tls=true, tls-verification=false, tag=%s",
			node.IP, node.Port, userUUID, node.Name)

	case "shadowsocks":
		return fmt.Sprintf("shadowsocks=%s:%d, method=%s, password=%s, tag=%s",
			node.IP, node.Port, node.SSMethod, node.SSPassword, node.Name)

	default:
		return ""
	}
}
