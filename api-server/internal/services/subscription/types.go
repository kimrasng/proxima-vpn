package subscription

type NodeInfo struct {
	Name             string
	IP               string
	Port             int
	Protocol         string // "vless_reality", "vmess_ws", "trojan_tls", "shadowsocks", "hysteria2", "wireguard"
	RealityPublicKey string
	RealityShortID   string
	WSPath           string
	SSMethod         string
	SSPassword       string
	TLSEnabled       bool
	ServerName       string
	WGPrivateKey     string
	WGPeerPublicKey  string
	WGAddress        string
	// NodeID and Tags identify the exit node a proxy belongs to, so routing
	// groups can take a specific node or every node carrying a tag.
	NodeID string
	Tags   []string
}
