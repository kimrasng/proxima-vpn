package devicebandwidth

import (
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"strings"
)

// IsDeviceOutboundTag reports whether tag names a per-device egress outbound.
func IsDeviceOutboundTag(tag string) bool {
	return strings.HasPrefix(tag, OutboundPrefix) && len(tag) > len(OutboundPrefix)
}

// DeviceUser is the Xray user email of a device: "<uuid>@proxima".
func DeviceUser(id string) string { return id + "@proxima" }

// IsDeviceRule reports whether a rule belongs to live-managed credential
// topology: a legacy exact user rule or a server-address-qualified rule whose
// outbound has a validated ~hex suffix and one exact host CIDR. Broader
// networks and other conditions remain structural.
func IsDeviceRule(rule map[string]json.RawMessage) bool {
	var outbound string
	_ = json.Unmarshal(rule["outboundTag"], &outbound)
	if !IsDeviceOutboundTag(outbound) {
		// Explicit per-credential deny remains live-managed when its tag and
		// exact authenticated user agree. Structural inbound fallback stays.
		var tag string
		var users []string
		_ = json.Unmarshal(rule["ruleTag"], &tag)
		if outbound != "block" || !strings.HasPrefix(tag, "client-address-deny-") || json.Unmarshal(rule["user"], &users) != nil || len(users) != 1 {
			return false
		}
		id := strings.TrimPrefix(tag, "client-address-deny-")
		if id == "" || users[0] != DeviceUser(id) {
			return false
		}
		for key := range rule {
			if key != "type" && key != "ruleTag" && key != "user" && key != "outboundTag" {
				return false
			}
		}
		return true
	}
	var users []string
	if err := json.Unmarshal(rule["user"], &users); err != nil || len(users) != 1 {
		return false
	}
	identity := strings.TrimPrefix(outbound, OutboundPrefix)
	id, key, qualified := strings.Cut(identity, "~")
	if users[0] != DeviceUser(id) {
		return false
	}
	if qualified {
		if len(key) < 16 || len(key) > 64 || len(key)%2 != 0 {
			return false
		}
		if _, err := hex.DecodeString(key); err != nil {
			return false
		}
		var sources []string
		if json.Unmarshal(rule["source"], &sources) != nil || len(sources) != 1 {
			return false
		}
		prefix, err := netip.ParsePrefix(sources[0])
		if err != nil || prefix.Bits() != prefix.Addr().BitLen() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
			return false
		}
	}
	for key := range rule {
		switch key {
		case "type", "user", "outboundTag", "ruleTag":
		case "source":
			if !qualified {
				return false
			}
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
