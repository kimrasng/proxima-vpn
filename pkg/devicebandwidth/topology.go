package devicebandwidth

import (
	"encoding/json"
	"strings"
)

// IsDeviceOutboundTag reports whether tag names a per-device egress outbound.
func IsDeviceOutboundTag(tag string) bool {
	return strings.HasPrefix(tag, OutboundPrefix) && len(tag) > len(OutboundPrefix)
}

// DeviceUser is the Xray user email of a device: "<uuid>@proxima".
func DeviceUser(id string) string { return id + "@proxima" }

// IsDeviceRule reports whether a routing rule is the exact per-device rule
// {user:["<uuid>@proxima"], outboundTag:"device-egress-<uuid>"} and nothing
// else. Any other condition makes it structural.
func IsDeviceRule(rule map[string]json.RawMessage) bool {
	var outbound string
	_ = json.Unmarshal(rule["outboundTag"], &outbound)
	if !IsDeviceOutboundTag(outbound) {
		return false
	}
	var users []string
	if err := json.Unmarshal(rule["user"], &users); err != nil || len(users) != 1 {
		return false
	}
	if users[0] != DeviceUser(strings.TrimPrefix(outbound, OutboundPrefix)) {
		return false
	}
	for key := range rule {
		switch key {
		case "type", "user", "outboundTag", "ruleTag":
		default:
			return false
		}
	}
	return true
}

// StripDeviceTopology removes per-device egress outbounds and their exact user
// rules from a decoded Xray config. They exist only because a device is
// admitted, so they are applied live with that device rather than treated as
// config structure; every other outbound and rule is left in place and order.
func StripDeviceTopology(cfg map[string]json.RawMessage) error {
	if raw, ok := cfg["outbounds"]; ok {
		var outbounds []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &outbounds); err != nil {
			return err
		}
		kept := outbounds[:0]
		for _, outbound := range outbounds {
			var tag string
			_ = json.Unmarshal(outbound["tag"], &tag)
			if !IsDeviceOutboundTag(tag) {
				kept = append(kept, outbound)
			}
		}
		encoded, err := json.Marshal(kept)
		if err != nil {
			return err
		}
		cfg["outbounds"] = encoded
	}
	if raw, ok := cfg["routing"]; ok {
		var routing map[string]json.RawMessage
		if err := json.Unmarshal(raw, &routing); err != nil {
			return err
		}
		if rulesRaw, ok := routing["rules"]; ok {
			var rules []map[string]json.RawMessage
			if err := json.Unmarshal(rulesRaw, &rules); err != nil {
				return err
			}
			kept := rules[:0]
			for _, rule := range rules {
				if !IsDeviceRule(rule) {
					kept = append(kept, rule)
				}
			}
			encoded, err := json.Marshal(kept)
			if err != nil {
				return err
			}
			routing["rules"] = encoded
			if cfg["routing"], err = json.Marshal(routing); err != nil {
				return err
			}
		}
	}
	return nil
}
