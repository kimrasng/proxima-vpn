package services

import (
	"encoding/json"
	"fmt"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

// WithClientAddressAllowlist replaces shared VLESS egress declarations only
// for credentials explicitly present in allowed. Missing keys retain legacy
// routing; a present empty list installs an explicit authenticated-user deny.
// Sources are transport
// peer addresses as Xray sees them, not original addresses behind masquerade.
// Callers must not apply original Entry-side addresses to relay peers.
//
// This pure builder is not automatically enabled: an initial admission and
// relay association protocol must exist before strict allowlisting is usable.
func WithClientAddressAllowlist(config []byte, allowed map[string][]string) ([]byte, error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("missing Xray configuration")
	}
	var outbounds []json.RawMessage
	if err := json.Unmarshal(cfg["outbounds"], &outbounds); err != nil {
		return nil, err
	}
	var routing map[string]json.RawMessage
	if err := json.Unmarshal(cfg["routing"], &routing); err != nil {
		return nil, err
	}
	var rules []json.RawMessage
	if err := json.Unmarshal(routing["rules"], &rules); err != nil {
		return nil, err
	}
	declared := map[string]bool{}
	controlled := map[string]bool{}
	var inbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(cfg["inbounds"], &inbounds); err != nil {
		return nil, err
	}
	for _, inbound := range inbounds {
		if inbound.Protocol == "vless" {
			controlled[inbound.Tag] = true
		}
	}
	blackholes := map[string]bool{}
	for _, raw := range outbounds {
		var outbound struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		}
		if err := json.Unmarshal(raw, &outbound); err != nil {
			return nil, err
		}
		if outbound.Protocol == "blackhole" {
			blackholes[outbound.Tag] = true
		}
	}
	covered := map[string]bool{}
	for _, raw := range rules {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rule); err != nil {
			return nil, err
		}
		var tag string
		var inbounds []string
		_ = json.Unmarshal(rule["outboundTag"], &tag)
		_ = json.Unmarshal(rule["inboundTag"], &inbounds)
		if blackholes[tag] && len(inbounds) > 0 && rule["user"] == nil && rule["source"] == nil {
			unconditional := true
			for key := range rule {
				if key != "type" && key != "inboundTag" && key != "outboundTag" && key != "ruleTag" {
					unconditional = false
				}
			}
			if unconditional {
				for _, tag := range inbounds {
					covered[tag] = true
				}
			}
		}
	}
	if len(allowed) > 0 {
		if len(controlled) == 0 {
			return nil, fmt.Errorf("no controlled VLESS inbounds")
		}
		for tag := range controlled {
			if !covered[tag] {
				return nil, fmt.Errorf("address allowlist requires deny fallback for inbound %s", tag)
			}
		}
	}
	keptOut := []json.RawMessage{}
	for _, raw := range outbounds {
		var outbound struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		}
		if err := json.Unmarshal(raw, &outbound); err != nil {
			return nil, err
		}
		remove := false
		for credential := range allowed {
			if outbound.Tag == devicebandwidth.OutboundPrefix+credential {
				if outbound.Protocol != "socks" {
					return nil, fmt.Errorf("invalid managed credential outbound")
				}
				declared[credential] = true
				remove = true
				break
			}
		}
		if !remove {
			keptOut = append(keptOut, raw)
		}
	}
	for credential := range allowed {
		if !declared[credential] {
			return nil, fmt.Errorf("allowlist credential has no shared declaration")
		}
	}
	replacementCounts := map[string]int{}
	keptRules := []json.RawMessage{}
	for _, raw := range rules {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rule); err != nil {
			return nil, err
		}
		var tag string
		_ = json.Unmarshal(rule["outboundTag"], &tag)
		replaced := false
		for credential, sources := range allowed {
			if tag != devicebandwidth.OutboundPrefix+credential {
				continue
			}
			if !devicebandwidth.IsDeviceRule(rule) {
				return nil, fmt.Errorf("cannot replace noncanonical credential route")
			}
			replacementCounts[credential]++
			if replacementCounts[credential] != 1 {
				return nil, fmt.Errorf("duplicate managed credential route")
			}
			if len(sources) == 0 {
				deny, err := json.Marshal(map[string]any{"type": "field", "user": []string{devicebandwidth.DeviceUser(credential)}, "outboundTag": "block", "ruleTag": "client-address-deny-" + credential})
				if err != nil {
					return nil, err
				}
				if !blackholes["block"] {
					return nil, fmt.Errorf("explicit credential deny requires block outbound")
				}
				keptRules = append(keptRules, deny)
			}
			out, sourceRules, err := devicebandwidth.AddressEgressDeclarations(credential, sources)
			if err != nil {
				return nil, err
			}
			keptOut = append(keptOut, out...)
			keptRules = append(keptRules, sourceRules...)
			replaced = true
			break
		}
		if !replaced {
			keptRules = append(keptRules, raw)
		}
	}
	for credential := range allowed {
		if replacementCounts[credential] != 1 {
			return nil, fmt.Errorf("allowlist credential has no canonical route")
		}
	}
	var err error
	cfg["outbounds"], err = json.Marshal(keptOut)
	if err != nil {
		return nil, err
	}
	routing["rules"], err = json.Marshal(keptRules)
	if err != nil {
		return nil, err
	}
	cfg["routing"], err = json.Marshal(routing)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(cfg, "", "  ")
}
