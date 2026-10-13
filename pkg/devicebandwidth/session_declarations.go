package devicebandwidth

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
)

// SessionEgressDeclarations creates one exact source/port route and outbound
// for a server-observed tunnel session. sessionKey is the stable 64-character
// SHA-256 token over Exit, UUID, peer/local tuple and accepted generation.
func SessionEgressDeclarations(credential, peerIP string, peerPort int, sessionKey string) (json.RawMessage, json.RawMessage, error) {
	if len(sessionKey) != 64 {
		return nil, nil, fmt.Errorf("invalid session token")
	}
	decoded, err := hex.DecodeString(sessionKey)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != sessionKey {
		return nil, nil, fmt.Errorf("invalid session token")
	}
	ip, err := netip.ParseAddr(peerIP)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || peerPort < 1 || peerPort > 65535 {
		return nil, nil, fmt.Errorf("invalid session peer tuple")
	}
	ip = ip.Unmap()
	prefix := netip.PrefixFrom(ip, ip.BitLen()).String()
	tag := OutboundPrefix + credential + "~" + sessionKey
	outbound := map[string]any{"tag": tag, "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": Port, "users": []any{map[string]any{"user": credential, "pass": "materialize-at-node"}}}}}}
	encoded, err := json.Marshal(outbound)
	if err != nil {
		return nil, nil, err
	}
	rule, err := json.Marshal(map[string]any{"type": "field", "user": []string{DeviceUser(credential)}, "source": []string{prefix}, "sourcePort": []string{strconv.Itoa(peerPort)}, "sessionToken": sessionKey, "outboundTag": tag})
	return encoded, rule, err
}
