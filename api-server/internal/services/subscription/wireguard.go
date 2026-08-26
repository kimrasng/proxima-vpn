package subscription

import (
	"fmt"
	"strings"
)

// GenerateWireGuardConf builds a standard wg-quick .conf file for a single
// WireGuard-capable node, for users of the plain WireGuard app (which cannot
// import a Clash/Sing-box config - see clashWireGuard/singboxWireGuard for
// the multi-protocol equivalents most clients should use instead).
func GenerateWireGuardConf(node NodeInfo) ([]byte, error) {
	if node.Protocol != "wireguard" {
		return nil, fmt.Errorf("node %q has no usable wireguard config", node.Name)
	}

	var sb strings.Builder
	sb.WriteString("[Interface]\n")
	fmt.Fprintf(&sb, "PrivateKey = %s\n", node.WGPrivateKey)
	fmt.Fprintf(&sb, "Address = %s\n", node.WGAddress)
	sb.WriteString("\n[Peer]\n")
	fmt.Fprintf(&sb, "PublicKey = %s\n", node.WGPeerPublicKey)
	fmt.Fprintf(&sb, "Endpoint = %s:%d\n", node.IP, node.Port)
	sb.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	sb.WriteString("PersistentKeepalive = 25\n")

	return []byte(sb.String()), nil
}
