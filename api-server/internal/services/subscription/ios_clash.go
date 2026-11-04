package subscription

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// IOSClashError identifies a profile field that cannot be rendered safely.
type IOSClashError struct {
	Path    string
	Message string
}

func (e *IOSClashError) Error() string {
	return fmt.Sprintf("iOS Clash %s: %s", e.Path, e.Message)
}

// GenerateIOSClash renders a TCP-only profile without Mihomo-only options.
// The caller must first transform remote providers into compatible YAML/text
// sources, including their contents; this renderer never fetches providers.
// Unsupported routing conditions and empty groups are errors, not fallbacks.
// BlockQUIC cannot be expressed exactly without logical rules: rejecting port
// 443 alone would also reject TCP HTTPS, so it is a hard incompatibility.
func GenerateIOSClash(nodes []NodeInfo, userUUID string, profile RoutingProfile) ([]byte, error) {
	if profile.BlockQUIC {
		return nil, &IOSClashError{Path: "block_quic", Message: "UDP port 443 blocking requires unsupported logical rules"}
	}
	if problems := validateMobileRoutingProfile(profile); len(problems) > 0 {
		return nil, &IOSClashError{Path: problems[0].Path, Message: problems[0].Message}
	}
	for i, provider := range profile.Providers {
		path := fmt.Sprintf("providers[%d]", i)
		if provider.Format == "mrs" {
			return nil, &IOSClashError{Path: path + ".format", Message: "mrs must be transformed before rendering"}
		}
		if provider.Type == "http" {
			u, err := url.Parse(provider.URL)
			if err != nil || strings.HasSuffix(strings.ToLower(u.Path), ".mrs") {
				return nil, &IOSClashError{Path: path + ".url", Message: "provider must use a compatible YAML or text source"}
			}
		}
		if provider.Type == "inline" && provider.Behavior == "classical" {
			for j, entry := range provider.Payload {
				parts := strings.Split(entry, ",")
				entryPath := fmt.Sprintf("%s.payload[%d]", path, j)
				if len(parts) < 2 || len(parts) > 4 || !ruleTypes[parts[0]] || parts[0] == "RULE-SET" {
					return nil, &IOSClashError{Path: entryPath, Message: "unsupported classical condition"}
				}
				if msg := ruleValueProblem(parts[0], parts[1], nil); msg != "" {
					return nil, &IOSClashError{Path: entryPath, Message: msg}
				}
				if len(parts) >= 3 && parts[2] == "" || len(parts) == 4 && parts[3] != "no-resolve" || (len(parts) == 3 && parts[2] == "no-resolve" || len(parts) == 4) && !noResolveTypes[parts[0]] {
					return nil, &IOSClashError{Path: entryPath, Message: "unsupported classical rule option"}
				}
			}
		}
	}

	_, proxyNames, kept, _, err := clashProxies(nodes, userUUID, nil)
	if err != nil {
		return nil, fmt.Errorf("iOS Clash proxies: %w", err)
	}
	members := resolveGroupMembers(profile, proxyNames, kept)
	required := map[string]bool{profile.Final: true}
	for _, rule := range profile.Rules {
		required[rule.Target] = true
	}
	for i, group := range profile.Groups {
		if len(members[group.Key]) == 0 && required[group.Key] {
			return nil, &IOSClashError{Path: fmt.Sprintf("groups[%d].members", i), Message: "rule target has no compatible members"}
		}
		if slices.Contains(proxyNames, group.Name) {
			return nil, &IOSClashError{Path: fmt.Sprintf("groups[%d].name", i), Message: "group name collides with a proxy"}
		}
	}
	seen := map[string]bool{}
	for i, name := range proxyNames {
		if groupNameProblem(name) != "" || seen[name] {
			return nil, &IOSClashError{Path: fmt.Sprintf("proxies[%d].name", i), Message: "proxy name must be unique and safe as a policy reference"}
		}
		seen[name] = true
	}

	// ExtraYAML may merge into hosts, which settingsDocument otherwise shares
	// with the caller's settings map.
	settings := profile.EffectiveSettings()
	settings.Hosts = maps.Clone(settings.Hosts)
	profile.Settings = &settings
	rendered, err := GenerateRoutedClash(nodes, userUUID, nil, profile)
	if err != nil {
		return nil, fmt.Errorf("iOS Clash render: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(rendered, &doc); err != nil {
		return nil, fmt.Errorf("iOS Clash document: %w", err)
	}
	// Allowlisting after the ExtraYAML merge also prevents unsupported options
	// from being reintroduced through the advanced settings editor.
	iosClashKeepKeys(doc, "mixed-port", "port", "socks-port", "allow-lan", "bind-address", "mode", "log-level", "ipv6", "hosts", "dns", "profile", "proxies", "proxy-groups", "rule-providers", "rules")
	dns, ok := doc["dns"].(map[string]any)
	if !ok {
		return nil, &IOSClashError{Path: "settings.extra_yaml", Message: "dns must be a mapping"}
	}
	iosClashKeepKeys(dns, "enable", "ipv6", "listen", "enhanced-mode", "default-nameserver", "nameserver", "fallback", "fallback-filter", "use-hosts")
	dns["enhanced-mode"] = "redir-host"
	if filter, exists := dns["fallback-filter"]; exists {
		m, ok := filter.(map[string]any)
		if !ok {
			return nil, &IOSClashError{Path: "settings.extra_yaml", Message: "fallback-filter must be a mapping"}
		}
		iosClashKeepKeys(m, "ipcidr", "domain")
		// Clash otherwise defaults the fallback filter to GeoIP matching.
		m["geoip"] = false
	} else if _, exists := dns["fallback"]; exists {
		dns["fallback-filter"] = map[string]any{"geoip": false}
	}
	if stored, exists := doc["profile"]; exists {
		m, ok := stored.(map[string]any)
		if !ok {
			return nil, &IOSClashError{Path: "settings.extra_yaml", Message: "profile must be a mapping"}
		}
		iosClashKeepKeys(m, "store-selected")
	}
	for _, proxy := range doc["proxies"].([]any) {
		m := proxy.(map[string]any)
		if reality, ok := m["reality-opts"].(map[string]any); ok {
			delete(reality, "support-x25519mlkem768")
		}
	}
	for _, group := range doc["proxy-groups"].([]any) {
		delete(group.(map[string]any), "hidden")
	}
	if providers, ok := doc["rule-providers"].(map[string]any); ok {
		ordered := &yaml.Node{Kind: yaml.MappingNode}
		for _, provider := range profile.Providers {
			value := providers[provider.Key].(map[string]any)
			delete(value, "proxy")
			var node yaml.Node
			if err := node.Encode(value); err != nil {
				return nil, fmt.Errorf("iOS Clash provider %s: %w", provider.Key, err)
			}
			ordered.Content = append(ordered.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: provider.Key}, &node)
		}
		doc["rule-providers"] = ordered
	}
	return marshalClashDocument(doc)
}

func iosClashKeepKeys(doc map[string]any, keys ...string) {
	for key := range doc {
		if !slices.Contains(keys, key) {
			delete(doc, key)
		}
	}
}
