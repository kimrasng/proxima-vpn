package devicebandwidth

import (
	"encoding/json"
	"fmt"
	"sort"
)

// AddressEgressDeclarations builds allowlisted source-qualified local egress
// declarations for one credential. Caller must append a controlled-inbound
// blackhole fallback and must never retain that credential's shared catchall.
// This builder does not recover original sources hidden behind NAT.
func AddressEgressDeclarations(credential string, addresses []string) ([]json.RawMessage, []json.RawMessage, error) {
	sources := append([]string(nil), addresses...)
	sort.Strings(sources)
	outbounds := []json.RawMessage{}
	rules := []json.RawMessage{}
	seen := map[string]bool{}
	for _, address := range sources {
		tag, prefix, err := AddressOutboundTag(credential, address)
		if err != nil {
			return nil, nil, err
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		outbound := map[string]any{"tag": tag, "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": Port, "users": []any{map[string]any{"user": credential, "pass": "materialize-at-node"}}}}}}
		encoded, err := json.Marshal(outbound)
		if err != nil {
			return nil, nil, fmt.Errorf("encode address outbound: %w", err)
		}
		outbounds = append(outbounds, encoded)
		rule, err := json.Marshal(map[string]any{"type": "field", "user": []string{DeviceUser(credential)}, "source": []string{prefix}, "outboundTag": tag})
		if err != nil {
			return nil, nil, err
		}
		rules = append(rules, rule)
	}
	return outbounds, rules, nil
}
