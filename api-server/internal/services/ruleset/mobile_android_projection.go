package ruleset

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
)

// ResolveAndroidProviders resolves providers using the mobile fetch limits and
// projects away PROCESS-NAME rules, providers emptied by that omission, and their
// RULE-SET references. Remaining rules and payloads retain their order and text.
// The input is not mutated; any other unsupported or incomplete data fails closed.
// ResolveMobileProviders retains its existing iOS behavior.
func ResolveAndroidProviders(ctx context.Context, profile subscription.RoutingProfile) (subscription.RoutingProfile, map[string][]string, error) {
	return resolveMobileProfile(ctx, profile, cachedMobileProvider, true)
}

func androidProcessEntry(entry string) bool {
	kind, _, found := strings.Cut(entry, ",")
	return found && strings.TrimSpace(kind) == "PROCESS-NAME"
}

func projectAndroidProviders(profile subscription.RoutingProfile, payloads map[string][]string) (subscription.RoutingProfile, map[string][]string, error) {
	removed := make(map[string]bool)
	providers := make([]subscription.RoutingProvider, 0, len(profile.Providers))
	for _, provider := range profile.Providers {
		payload := payloads[provider.Key]
		if provider.Behavior == "classical" {
			filtered := make([]string, 0, len(payload))
			for _, entry := range payload {
				if !androidProcessEntry(entry) {
					filtered = append(filtered, entry)
				}
			}
			payload = filtered
		}
		if len(payload) == 0 {
			removed[provider.Key] = true
			delete(payloads, provider.Key)
			continue
		}
		payloads[provider.Key] = payload
		if provider.Type == "inline" {
			provider.Payload = append([]string(nil), payload...)
		}
		providers = append(providers, provider)
	}
	profile.Providers = providers
	rules := make([]subscription.RoutingRule, 0, len(profile.Rules))
	for _, rule := range profile.Rules {
		if rule.Type == "PROCESS-NAME" || (rule.Type == "RULE-SET" && removed[rule.Value]) {
			continue
		}
		rules = append(rules, rule)
	}
	profile.Rules = rules
	return profile, payloads, nil
}

func androidEntryProblem(behavior, entry string) error {
	if behavior != "classical" {
		return mobileEntryProblem(behavior, entry)
	}
	if entry == "" || len(entry) > 512 || strings.IndexFunc(entry, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid Android classical entry %q", entry)
	}
	kind, rest, _ := strings.Cut(entry, ",")
	if kind == "PROCESS-NAME" {
		parts := strings.Split(rest, ",")
		if len(parts) > 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[0]) != parts[0] || (len(parts) == 2 && (strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[1]) != parts[1] || parts[1] == "no-resolve")) {
			return fmt.Errorf("invalid process rule %q", entry)
		}
		return nil
	}
	if kind == "NETWORK" {
		parts := strings.Split(rest, ",")
		if len(parts) > 2 || (parts[0] != "UDP" && parts[0] != "TCP") || (len(parts) == 2 && (strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[1]) != parts[1] || parts[1] == "no-resolve")) {
			return fmt.Errorf("invalid network rule %q", entry)
		}
		return nil
	}
	if kind != "AND" {
		return mobileEntryProblem(behavior, entry)
	}
	// AND supports flat conditions with distinct dimensions. Repeating a
	// dimension would turn an intersection into an OR in Android's rule arrays.
	if !strings.HasPrefix(rest, "((") {
		return fmt.Errorf("invalid AND rule %q", entry)
	}
	end := strings.Index(rest, "))")
	if end < 0 {
		return fmt.Errorf("invalid AND rule %q", entry)
	}
	tail := rest[end+2:]
	if tail != "" && (!strings.HasPrefix(tail, ",") || strings.TrimSpace(tail[1:]) == "" || strings.TrimSpace(tail[1:]) != tail[1:] || strings.ContainsAny(tail[1:], ",()") || tail[1:] == "no-resolve") {
		return fmt.Errorf("invalid AND target %q", entry)
	}
	conditions := strings.Split(rest[2:end], "),(")
	if len(conditions) < 2 {
		return fmt.Errorf("AND requires multiple conditions: %q", entry)
	}
	seen := make(map[string]bool)
	for _, condition := range conditions {
		parts := strings.Split(condition, ",")
		if len(parts) != 2 || strings.ContainsAny(condition, "()") || parts[0] == "AND" || parts[0] == "PROCESS-NAME" {
			return fmt.Errorf("invalid AND condition %q", condition)
		}
		dimension := parts[0]
		switch dimension {
		case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD":
			dimension = "domain"
		case "IP-CIDR", "IP-CIDR6":
			dimension = "ip"
		}
		if seen[dimension] {
			return fmt.Errorf("repeated AND dimension %q", dimension)
		}
		seen[dimension] = true
		if err := androidEntryProblem("classical", condition); err != nil {
			return err
		}
	}
	return nil
}
