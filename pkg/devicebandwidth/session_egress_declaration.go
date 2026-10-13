package devicebandwidth

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
)

// SessionEgressDeclaration creates one source+port-qualified outbound for an
// authenticated transport session. The stable token is server-generated from
// Exit identity, credential UUID, local/peer tuple and accepted generation.
func SessionEgressDeclaration(credential string, peer netip.AddrPort, localIP string, token string) (json.RawMessage, json.RawMessage, error) {
	if len(token) != 64 {
		return nil, nil, fmt.Errorf("invalid session token")
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != token {
		return nil, nil, fmt.Errorf("invalid session token")
	}
	if !peer.IsValid() || peer.Port() == 0 || peer.Addr().Zone() != "" || peer.Addr().IsUnspecified() || peer.Addr().IsMulticast() {
		return nil, nil, fmt.Errorf("invalid session peer")
	}
	if local, err := netip.ParseAddr(localIP); err != nil || local.Zone() != "" || local.IsUnspecified() || local.IsMulticast() {
		return nil, nil, fmt.Errorf("invalid session local address")
	}
	peerIP := peer.Addr().Unmap()
	tag := OutboundPrefix + credential + "~" + token
	outbound := map[string]any{"tag": tag, "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": Port, "users": []any{map[string]any{"user": credential, "pass": "materialize-at-node"}}}}}}
	encoded, err := json.Marshal(outbound)
	if err != nil {
		return nil, nil, err
	}
	rule, err := json.Marshal(map[string]any{"type": "field", "user": []string{DeviceUser(credential)}, "source": []string{netip.PrefixFrom(peerIP, peerIP.BitLen()).String()}, "sourcePort": []string{strconv.Itoa(int(peer.Port()))}, "sessionToken": token, "outboundTag": tag})
	return encoded, rule, err
}
