package subscription

import (
	"fmt"
	"strings"
)

func GenerateSurfboard(nodes []NodeInfo, userUUID string) ([]byte, error) {
	var proxyLines []string
	var nodeNames []string

	for _, node := range nodes {
		if node.Protocol == "vless_reality" {
			continue
		}

		line := buildSurfboardProxyLine(node, userUUID)
		if line == "" {
			continue
		}

		proxyLines = append(proxyLines, line)
		nodeNames = append(nodeNames, node.Name)
	}

	if len(proxyLines) == 0 {
		return []byte(""), nil
	}

	var sb strings.Builder

	sb.WriteString("[General]\n")
	sb.WriteString("loglevel = notify\n")
	sb.WriteString("skip-proxy = 127.0.0.1, 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local\n")
	sb.WriteString("dns-server = system, 8.8.8.8, 8.8.4.4\n")
	sb.WriteString("\n")

	sb.WriteString("[Proxy]\n")
	sb.WriteString("DIRECT = direct\n")
	for _, line := range proxyLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	sb.WriteString("[Proxy Group]\n")
	allNodes := strings.Join(nodeNames, ", ")
	sb.WriteString(fmt.Sprintf("Auto = url-test, %s, url=http://www.gstatic.com/generate_204, interval=300\n", allNodes))
	sb.WriteString(fmt.Sprintf("Select = select, Auto, %s\n", allNodes))
	sb.WriteString("\n")

	sb.WriteString("[Rule]\n")
	sb.WriteString("FINAL,Select\n")

	return []byte(sb.String()), nil
}

func buildSurfboardProxyLine(node NodeInfo, userUUID string) string {
	switch node.Protocol {
	case "vmess":
		parts := []string{
			fmt.Sprintf("%s = vmess, %s, %d, username=%s", node.Name, node.IP, node.Port, userUUID),
		}
		if node.WSPath != "" {
			parts = append(parts, "ws=true", fmt.Sprintf("ws-path=%s", node.WSPath))
		}
		if node.TLSEnabled {
			parts = append(parts, "tls=true")
			if node.ServerName != "" {
				parts = append(parts, fmt.Sprintf("sni=%s", node.ServerName))
			}
		}
		return strings.Join(parts, ", ")

	case "trojan":
		sni := node.ServerName
		if sni == "" {
			sni = node.IP
		}
		return fmt.Sprintf("%s = trojan, %s, %d, password=%s, sni=%s", node.Name, node.IP, node.Port, userUUID, sni)

	case "shadowsocks":
		return fmt.Sprintf("%s = ss, %s, %d, encrypt-method=%s, password=%s", node.Name, node.IP, node.Port, node.SSMethod, node.SSPassword)

	default:
		return ""
	}
}
