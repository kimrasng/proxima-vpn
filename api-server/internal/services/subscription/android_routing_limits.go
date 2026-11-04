package subscription

import "strings"

const (
	maxAndroidRoutingEntries = 250000
	maxAndroidRoutingBytes   = 32 << 20
	maxAndroidRoutingNodes   = 4096
)

// Check repeated references before allocating resolved policies, expanded
// matches or JSON. Byte accounting is an upper bound: JSON escaping takes at
// most six bytes per input byte; every output rule is charged for all possible
// candidates even when its actual policy is smaller or direct/block.
func checkAndroidRoutingBudget(profile RoutingProfile, providers map[string][]string, nodes []NodeInfo) error {
	problem := func() error {
		return androidRoutingProblem("budget", "routing exceeds 250000 expanded entries, 32 MiB estimated JSON, or 4096 input nodes")
	}
	if len(nodes) > maxAndroidRoutingNodes || len(profile.Groups) > maxRoutingGroups || len(profile.Providers) > maxRoutingProviders || len(profile.Rules) > maxRoutingRules {
		return problem()
	}
	payloadFor := func(key string) ([]string, string) {
		for _, provider := range profile.Providers {
			if provider.Key == key {
				if provider.Type == "inline" {
					return provider.Payload, provider.Behavior
				}
				return providers[key], provider.Behavior
			}
		}
		return nil, ""
	}
	remainingEntries := maxAndroidRoutingEntries
	for _, rule := range profile.Rules {
		count := 1
		if rule.Type == "RULE-SET" {
			payload, _ := payloadFor(rule.Value)
			count = len(payload)
		}
		if count > remainingEntries {
			return problem()
		}
		remainingEntries -= count
	}
	remaining := maxAndroidRoutingBytes
	charge := func(size int) bool {
		if size > remaining {
			return false
		}
		remaining -= size
		return true
	}
	text := func(value string) bool {
		if len(value) > remaining/6 {
			return false
		}
		return charge(len(value)*6 + 16)
	}
	candidateBytes := 0
	for _, node := range nodes {
		if node.Protocol != "vless_reality" {
			continue
		}
		if len(node.Name) > (maxAndroidRoutingBytes-candidateBytes-16)/6 {
			return problem()
		}
		candidateBytes += len(node.Name)*6 + 16
	}
	// Includes document keys, QUIC rule, final rule and default probe URL.
	if !charge(1024 + candidateBytes) {
		return problem()
	}
	for _, group := range profile.Groups {
		if !text(group.URL) {
			return problem()
		}
	}
	for _, rule := range profile.Rules {
		if rule.Type != "RULE-SET" {
			if !charge(512+candidateBytes) || !text(rule.Value) {
				return problem()
			}
			continue
		}
		payload, behavior := payloadFor(rule.Value)
		previous := ""
		for _, entry := range payload {
			if !text(entry) {
				return problem()
			}
			kind := behavior
			if behavior == "classical" {
				kind, _, _ = strings.Cut(strings.TrimSpace(entry), ",")
				switch strings.TrimSpace(kind) {
				case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD":
					kind = "domain"
				case "IP-CIDR", "IP-CIDR6":
					kind = "ipcidr"
				default:
					kind = "other"
				}
			}
			// Explicit classical targets can split every adjacent predicate
			// into a separate policy. Charge each before resolving any target.
			if behavior == "classical" || kind != previous || (kind != "domain" && kind != "ipcidr") {
				if !charge(512 + candidateBytes) {
					return problem()
				}
			}
			previous = kind
		}
	}
	return nil
}
