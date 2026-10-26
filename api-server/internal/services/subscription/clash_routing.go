package subscription

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// GenerateRoutedClash renders a Clash Meta profile whose groups, rule sets and
// rules come from profile. Groups that end up with no members (for example no
// published node carries the group's tag) are dropped, and anything that pointed
// at them falls back to the final group, then to DIRECT.
func GenerateRoutedClash(nodes []NodeInfo, userUUID string, infoLabels []string, profile RoutingProfile) ([]byte, error) {
	proxies, proxyNames, kept, infoNames, err := clashProxies(nodes, userUUID, infoLabels)
	if err != nil {
		return nil, err
	}

	members := resolveGroupMembers(profile, proxyNames, kept)
	names := map[string]string{}
	var groups []proxyGroup
	if len(infoNames) > 0 {
		groups = append(groups, proxyGroup{Name: groupInfo, Type: "select", Proxies: infoNames})
	}
	for _, g := range profile.Groups {
		if len(members[g.Key]) == 0 {
			continue
		}
		names[g.Key] = g.Name
	}
	for _, g := range profile.Groups {
		if _, ok := names[g.Key]; !ok {
			continue
		}
		pg := proxyGroup{Name: g.Name, Type: g.Type, Hidden: g.Hidden}
		for _, m := range members[g.Key] {
			if m.group != "" {
				pg.Proxies = append(pg.Proxies, names[m.group])
			} else {
				pg.Proxies = append(pg.Proxies, m.proxy)
			}
		}
		if g.Type == "url-test" || g.Type == "fallback" {
			pg.URL = orDefault(g.URL, defaultHealthURL)
			pg.Interval = orDefaultInt(g.Interval, defaultHealthInterval)
			pg.Lazy = true
			if g.Type == "url-test" {
				pg.Tolerance = orDefaultInt(g.Tolerance, defaultTolerance)
			}
		}
		groups = append(groups, pg)
	}

	finalTarget := "DIRECT"
	if name, ok := names[profile.Final]; ok {
		finalTarget = name
	}
	resolveTarget := func(target string) string {
		if target == "DIRECT" || target == "REJECT" {
			return target
		}
		if name, ok := names[target]; ok {
			return name
		}
		return finalTarget
	}

	providers := map[string]map[string]any{}
	for _, p := range profile.Providers {
		providers[p.Key] = clashRuleProvider(p)
	}

	var rules []string
	if profile.BlockQUIC {
		rules = append(rules, "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT")
	}
	for _, r := range profile.Rules {
		line := fmt.Sprintf("%s,%s,%s", r.Type, r.Value, resolveTarget(r.Target))
		if r.NoResolve {
			line += ",no-resolve"
		}
		rules = append(rules, line)
	}
	rules = append(rules, "MATCH,"+finalTarget)

	fakeIPFilter := []string{}
	for _, p := range profile.Providers {
		if p.Key == "private_domain" && p.Behavior == "domain" {
			fakeIPFilter = append(fakeIPFilter, "RULE-SET,private_domain,real-ip")
		}
	}
	fakeIPFilter = append(fakeIPFilter, "MATCH,fake-ip")

	settings := profile.EffectiveSettings()
	doc := settingsDocument(settings, fakeIPFilter)
	if strings.TrimSpace(settings.ExtraYAML) != "" {
		var extra map[string]any
		if err := yaml.Unmarshal([]byte(settings.ExtraYAML), &extra); err != nil {
			return nil, fmt.Errorf("extra yaml: %w", err)
		}
		mergeYAML(doc, extra)
	}
	doc["proxies"] = proxies
	doc["proxy-groups"] = groups
	if len(providers) > 0 {
		doc["rule-providers"] = providers
	}
	doc["rules"] = rules
	return marshalClashDocument(doc)
}

// clashKeyOrder lists top-level keys in the order Clash documentation uses, so
// the rendered profile reads naturally; unknown keys from extra YAML follow.
var clashKeyOrder = []string{
	"mixed-port", "port", "socks-port", "redir-port", "tproxy-port", "allow-lan", "bind-address",
	"mode", "log-level", "ipv6", "unified-delay", "tcp-concurrent", "find-process-mode",
	"keep-alive-interval", "keep-alive-idle", "disable-keep-alive", "external-controller", "secret",
	"geodata-mode", "geo-auto-update", "geo-update-interval", "geox-url", "profile", "tun", "sniffer",
	"hosts", "dns", "proxies", "proxy-groups", "rule-providers", "rules",
}

func marshalClashDocument(doc map[string]any) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k string) error {
		var v yaml.Node
		if err := v.Encode(doc[k]); err != nil {
			return err
		}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &v)
		delete(doc, k)
		return nil
	}
	tail := []string{"proxies", "proxy-groups", "rule-providers", "rules"}
	for _, k := range clashKeyOrder {
		if slices.Contains(tail, k) {
			continue
		}
		if _, ok := doc[k]; ok {
			if err := add(k); err != nil {
				return nil, err
			}
		}
	}
	var rest []string
	for k := range doc {
		if !slices.Contains(tail, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	for _, k := range append(rest, tail...) {
		if _, ok := doc[k]; ok {
			if err := add(k); err != nil {
				return nil, err
			}
		}
	}
	return yaml.Marshal(root)
}

type resolvedMember struct {
	group string
	proxy string
}

// resolveGroupMembers expands node filters into proxy names and drops
// references to groups that resolve to nothing, repeating until stable so an
// empty group empties the groups that only pointed at it. Validation
// guarantees the references form a DAG.
func resolveGroupMembers(profile RoutingProfile, proxyNames []string, kept []NodeInfo) map[string][]resolvedMember {
	out := map[string][]resolvedMember{}
	alive := map[string]bool{}
	for _, g := range profile.Groups {
		alive[g.Key] = true
	}
	for changed := true; changed; {
		changed = false
		for _, g := range profile.Groups {
			if !alive[g.Key] {
				continue
			}
			var resolved []resolvedMember
			seen := map[string]bool{}
			addProxy := func(name string) {
				if !seen["p:"+name] {
					seen["p:"+name] = true
					resolved = append(resolved, resolvedMember{proxy: name})
				}
			}
			for _, m := range g.Members {
				switch m.Kind {
				case MemberGroup:
					if alive[m.Ref] && !seen["g:"+m.Ref] {
						seen["g:"+m.Ref] = true
						resolved = append(resolved, resolvedMember{group: m.Ref})
					}
				case MemberBuiltin:
					addProxy(m.Value)
				case MemberTag, MemberNode, MemberAll:
					for i, node := range kept {
						if nodeMatches(node, m) {
							addProxy(proxyNames[i])
						}
					}
				}
			}
			if len(resolved) == 0 {
				alive[g.Key] = false
				changed = true
				delete(out, g.Key)
				continue
			}
			out[g.Key] = resolved
		}
	}
	return out
}

func nodeMatches(node NodeInfo, m RoutingMember) bool {
	switch m.Kind {
	case MemberAll:
		return true
	case MemberNode:
		return node.NodeID != "" && strings.EqualFold(node.NodeID, m.NodeID)
	case MemberTag:
		return slices.Contains(node.Tags, m.Tag)
	}
	return false
}

func clashRuleProvider(p RoutingProvider) map[string]any {
	out := map[string]any{"type": p.Type, "behavior": p.Behavior}
	if p.Type == "inline" {
		out["payload"] = p.Payload
		return out
	}
	format := orDefault(p.Format, "yaml")
	out["format"] = format
	out["url"] = p.URL
	out["interval"] = orDefaultInt(p.Interval, defaultRuleInterval)
	out["path"] = fmt.Sprintf("./ruleset/%s.%s", p.Key, map[string]string{"mrs": "mrs", "yaml": "yaml", "text": "list"}[format])
	// Downloaded directly: before providers load, an unproxied fetch would
	// otherwise fall through to MATCH and depend on the selected node.
	out["proxy"] = "DIRECT"
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
