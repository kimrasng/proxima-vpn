package subscription

import "encoding/json"

func GenerateSingbox(nodes []NodeInfo, userUUID string) ([]byte, error) {
	var nodeOutbounds []map[string]interface{}
	var nodeTags []string

	for _, node := range nodes {
		ob := singboxOutbound(node, userUUID)
		if ob == nil {
			continue
		}
		nodeOutbounds = append(nodeOutbounds, ob)
		nodeTags = append(nodeTags, node.Name)
	}

	selectorOutbounds := append([]string{"auto"}, nodeTags...)
	selectorOutbounds = append(selectorOutbounds, "direct")

	selector := map[string]interface{}{
		"type":      "selector",
		"tag":       "select",
		"outbounds": selectorOutbounds,
	}

	urltest := map[string]interface{}{
		"type":      "urltest",
		"tag":       "auto",
		"outbounds": nodeTags,
		"interval":  "5m",
	}

	direct := map[string]interface{}{
		"type": "direct",
		"tag":  "direct",
	}

	outbounds := []map[string]interface{}{selector, urltest}
	outbounds = append(outbounds, nodeOutbounds...)
	outbounds = append(outbounds, direct)

	config := map[string]interface{}{
		"outbounds": outbounds,
	}

	return json.MarshalIndent(config, "", "  ")
}

func singboxOutbound(node NodeInfo, userUUID string) map[string]interface{} {
	switch node.Protocol {
	case "vless_reality":
		return singboxVLESS(node, userUUID)
	case "vmess_ws":
		return singboxVMess(node, userUUID)
	case "trojan_tls":
		return singboxTrojan(node, userUUID)
	case "shadowsocks":
		return singboxShadowsocks(node)
	case "hysteria2":
		return singboxHysteria2(node, userUUID)
	case "wireguard":
		return singboxWireGuard(node)
	default:
		return nil
	}
}

func singboxVLESS(node NodeInfo, userUUID string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "vless",
		"tag":         node.Name,
		"server":      node.IP,
		"server_port": node.Port,
		"uuid":        userUUID,
		"flow":        "xtls-rprx-vision",
		"tls": map[string]interface{}{
			"enabled":     true,
			"server_name": "www.microsoft.com",
			"utls": map[string]interface{}{
				"enabled":     true,
				"fingerprint": "chrome",
			},
			"reality": map[string]interface{}{
				"enabled":    true,
				"public_key": node.RealityPublicKey,
				"short_id":   node.RealityShortID,
			},
		},
	}
}

func singboxVMess(node NodeInfo, userUUID string) map[string]interface{} {
	wsPath := node.WSPath
	if wsPath == "" {
		wsPath = "/vmess"
	}

	return map[string]interface{}{
		"type":        "vmess",
		"tag":         node.Name,
		"server":      node.IP,
		"server_port": node.Port,
		"uuid":        userUUID,
		"security":    "auto",
		"alter_id":    0,
		"transport": map[string]interface{}{
			"type": "ws",
			"path": wsPath,
		},
		"tls": map[string]interface{}{
			"enabled": true,
		},
	}
}

func singboxTrojan(node NodeInfo, userUUID string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "trojan",
		"tag":         node.Name,
		"server":      node.IP,
		"server_port": node.Port,
		"password":    userUUID,
		"tls": map[string]interface{}{
			"enabled": true,
		},
	}
}

func singboxShadowsocks(node NodeInfo) map[string]interface{} {
	method := node.SSMethod
	if method == "" {
		method = "2022-blake3-aes-128-gcm"
	}

	return map[string]interface{}{
		"type":        "shadowsocks",
		"tag":         node.Name,
		"server":      node.IP,
		"server_port": node.Port,
		"method":      method,
		"password":    node.SSPassword,
	}
}

func singboxHysteria2(node NodeInfo, userUUID string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "hysteria2",
		"tag":         node.Name,
		"server":      node.IP,
		"server_port": node.Port,
		"password":    userUUID,
		"tls": map[string]interface{}{
			"enabled": true,
		},
	}
}

func singboxWireGuard(node NodeInfo) map[string]interface{} {
	return map[string]interface{}{
		"type":            "wireguard",
		"tag":             node.Name,
		"server":          node.IP,
		"server_port":     node.Port,
		"private_key":     node.WGPrivateKey,
		"peer_public_key": node.WGPeerPublicKey,
		"local_address":   []string{node.WGAddress},
	}
}
