package subscription

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// RoutingProfile is the admin-editable description of a Clash Meta routing
// layout. Groups, providers and rules reference each other by key; names are
// what clients display.
type RoutingProfile struct {
	BlockQUIC bool              `json:"block_quic"`
	Final     string            `json:"final"`
	Groups    []RoutingGroup    `json:"groups"`
	Providers []RoutingProvider `json:"providers"`
	Rules     []RoutingRule     `json:"rules"`
	// Settings is nil in documents saved before settings were editable;
	// EffectiveSettings fills the defaults in.
	Settings *ClashSettings `json:"settings,omitempty"`
}

// EffectiveSettings returns the profile's Clash settings, or the defaults when
// the profile predates them.
func (p RoutingProfile) EffectiveSettings() ClashSettings {
	if p.Settings == nil {
		return DefaultClashSettings()
	}
	return *p.Settings
}

type RoutingGroup struct {
	Key       string          `json:"key"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Hidden    bool            `json:"hidden"`
	URL       string          `json:"url,omitempty"`
	Interval  int             `json:"interval,omitempty"`
	Tolerance int             `json:"tolerance,omitempty"`
	Members   []RoutingMember `json:"members"`
}

// RoutingMember is one entry of a group: another group (Ref), a built-in
// policy (Value), every node carrying a tag (Tag), one node (NodeID), or
// every node (all).
type RoutingMember struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref,omitempty"`
	Value  string `json:"value,omitempty"`
	Tag    string `json:"tag,omitempty"`
	NodeID string `json:"node_id,omitempty"`
}

type RoutingProvider struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Behavior string   `json:"behavior"`
	Format   string   `json:"format,omitempty"`
	URL      string   `json:"url,omitempty"`
	Interval int      `json:"interval,omitempty"`
	Payload  []string `json:"payload,omitempty"`
}

type RoutingRule struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Target    string `json:"target"`
	NoResolve bool   `json:"no_resolve,omitempty"`
}

const (
	MemberGroup   = "group"
	MemberBuiltin = "builtin"
	MemberTag     = "tag"
	MemberNode    = "node"
	MemberAll     = "all"

	defaultHealthURL      = "https://cp.cloudflare.com/generate_204"
	defaultHealthInterval = 300
	defaultTolerance      = 50
	defaultRuleInterval   = 86400

	maxRoutingGroups    = 64
	maxRoutingProviders = 64
	maxRoutingRules     = 500
	maxInlinePayload    = 5000
	maxNodeTags         = 16
)

// MetaRulesBase is the jsDelivr mirror of MetaCubeX/meta-rules-dat's meta
// branch, where the per-category geosite/geoip rule sets live.
const MetaRulesBase = "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@meta/geo"

func metaRuleSet(key, kind, name, behavior string) RoutingProvider {
	return RoutingProvider{
		Key: key, Type: "http", Behavior: behavior, Format: "mrs",
		URL: fmt.Sprintf("%s/%s/%s.mrs", MetaRulesBase, kind, name), Interval: defaultRuleInterval,
	}
}

func groupRef(key string) RoutingMember { return RoutingMember{Kind: MemberGroup, Ref: key} }
func builtin(v string) RoutingMember    { return RoutingMember{Kind: MemberBuiltin, Value: v} }
func tagged(tag string) RoutingMember   { return RoutingMember{Kind: MemberTag, Tag: tag} }
func allNodes() RoutingMember           { return RoutingMember{Kind: MemberAll} }

// DefaultRoutingProfile sends Korean services to exits tagged kr-home
// (residential Korean lines) and everything else to the fastest exit tagged
// jp; ad blocking is opt-in. Groups whose tags no node carries are dropped.
func DefaultRoutingProfile() RoutingProfile {
	settings := DefaultClashSettings()
	return RoutingProfile{
		Settings:  &settings,
		BlockQUIC: true,
		Final:     "overseas",
		Groups: []RoutingGroup{
			{Key: "kr_service", Name: "🇰🇷 한국 서비스", Type: "select",
				Members: []RoutingMember{groupRef("kr_home"), groupRef("kr_all"), groupRef("jp_auto"), builtin("DIRECT")}},
			{Key: "overseas", Name: "🌐 해외 트래픽", Type: "select",
				Members: []RoutingMember{groupRef("jp_auto"), groupRef("manual"), builtin("DIRECT")}},
			{Key: "manual", Name: groupSelect, Type: "select",
				Members: []RoutingMember{groupRef("jp_auto"), groupRef("kr_home"), allNodes()}},
			{Key: "kr_home", Name: "🏠 한국 가정망", Type: "fallback",
				Members: []RoutingMember{tagged("kr-home")}},
			{Key: "kr_all", Name: "🇰🇷 한국 전체", Type: "url-test", Hidden: true,
				Members: []RoutingMember{tagged("kr-home"), tagged("kr")}},
			{Key: "jp_auto", Name: "🇯🇵 일본 자동", Type: "url-test",
				Members: []RoutingMember{tagged("jp")}},
			{Key: "ads", Name: "🛑 광고 차단", Type: "select",
				Members: []RoutingMember{groupRef("overseas"), builtin("REJECT")}},
		},
		Providers: []RoutingProvider{
			metaRuleSet("private_domain", "geosite", "private", "domain"),
			metaRuleSet("private_ip", "geoip", "private", "ipcidr"),
			metaRuleSet("ads", "geosite", "category-ads-all", "domain"),
			{Key: "kr_custom", Type: "inline", Behavior: "domain", Payload: []string{
				"+.kr", "+.tving.com", "+.wavve.com", "+.coupangplay.com", "+.watcha.com", "+.laftel.net",
				"+.kbstar.com", "+.shinhan.com", "+.wooribank.com", "+.hanabank.com", "+.nonghyup.com",
				"+.ibk.co.kr", "+.toss.im", "+.tossbank.com", "+.kakaobank.com", "+.daum.net",
				"+.melon.com", "+.nexon.com", "+.afreecatv.com", "+.sooplive.co.kr",
			}},
			metaRuleSet("kr_naver", "geosite", "naver", "domain"),
			metaRuleSet("kr_kakao", "geosite", "kakao", "domain"),
			metaRuleSet("kr_coupang", "geosite", "coupang", "domain"),
			metaRuleSet("kr_ip", "geoip", "kr", "ipcidr"),
		},
		Rules: []RoutingRule{
			{Type: "RULE-SET", Value: "private_domain", Target: "DIRECT"},
			{Type: "RULE-SET", Value: "private_ip", Target: "DIRECT", NoResolve: true},
			{Type: "RULE-SET", Value: "ads", Target: "ads"},
			{Type: "RULE-SET", Value: "kr_custom", Target: "kr_service"},
			{Type: "RULE-SET", Value: "kr_naver", Target: "kr_service"},
			{Type: "RULE-SET", Value: "kr_kakao", Target: "kr_service"},
			{Type: "RULE-SET", Value: "kr_coupang", Target: "kr_service"},
			{Type: "RULE-SET", Value: "kr_ip", Target: "kr_service"},
		},
	}
}

// FieldError addresses one invalid value by its JSON path, e.g. "rules[3].target".
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

var (
	routingKeyPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	// NodeTagPattern bounds node tags so a tag is safe as a JSON value, a
	// query parameter and a UI chip.
	NodeTagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reservedNames  = map[string]bool{"DIRECT": true, "REJECT": true, "MATCH": true, "GLOBAL": true, "PASS": true}
	ruleTypes      = map[string]bool{"RULE-SET": true, "DOMAIN": true, "DOMAIN-SUFFIX": true, "DOMAIN-KEYWORD": true, "IP-CIDR": true, "IP-CIDR6": true, "DST-PORT": true}
	noResolveTypes = map[string]bool{"RULE-SET": true, "IP-CIDR": true, "IP-CIDR6": true}
)

// ValidateRoutingProfile reports every problem it finds; an empty result means
// the profile can be stored and rendered. Values end up inside comma-separated
// Clash rule strings, so separators are rejected rather than escaped.
func ValidateRoutingProfile(p RoutingProfile) []FieldError {
	var errs []FieldError
	add := func(path, format string, args ...any) {
		errs = append(errs, FieldError{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	if len(p.Groups) == 0 {
		add("groups", "at least one group is required")
	}
	if len(p.Groups) > maxRoutingGroups {
		add("groups", "at most %d groups", maxRoutingGroups)
	}
	if len(p.Providers) > maxRoutingProviders {
		add("providers", "at most %d rule sets", maxRoutingProviders)
	}
	if len(p.Rules) > maxRoutingRules {
		add("rules", "at most %d rules", maxRoutingRules)
	}

	groups := map[string]RoutingGroup{}
	names := map[string]bool{}
	for i, g := range p.Groups {
		path := fmt.Sprintf("groups[%d]", i)
		if !routingKeyPattern.MatchString(g.Key) {
			add(path+".key", "key must match ^[a-z0-9_-]{1,32}$")
		} else if _, dup := groups[g.Key]; dup {
			add(path+".key", "duplicate group key %q", g.Key)
		} else {
			groups[g.Key] = g
		}
		if msg := groupNameProblem(g.Name); msg != "" {
			add(path+".name", "%s", msg)
		} else if names[g.Name] {
			add(path+".name", "duplicate group name %q", g.Name)
		} else {
			names[g.Name] = true
		}
		switch g.Type {
		case "select":
		case "url-test", "fallback":
			if g.URL != "" && !strings.HasPrefix(g.URL, "http://") && !strings.HasPrefix(g.URL, "https://") {
				add(path+".url", "health check URL must be http(s)")
			}
			if g.Interval != 0 && (g.Interval < 30 || g.Interval > 86400) {
				add(path+".interval", "interval must be 30-86400 seconds")
			}
			if g.Tolerance < 0 || g.Tolerance > 5000 {
				add(path+".tolerance", "tolerance must be 0-5000 ms")
			}
		default:
			add(path+".type", "type must be select, url-test or fallback")
		}
		if len(g.Members) == 0 {
			add(path+".members", "a group needs at least one member")
		}
	}

	for i, g := range p.Groups {
		for j, m := range g.Members {
			path := fmt.Sprintf("groups[%d].members[%d]", i, j)
			switch m.Kind {
			case MemberGroup:
				if m.Ref == g.Key {
					add(path+".ref", "a group cannot contain itself")
				} else if _, ok := groups[m.Ref]; !ok {
					add(path+".ref", "unknown group %q", m.Ref)
				}
			case MemberBuiltin:
				if m.Value != "DIRECT" && m.Value != "REJECT" {
					add(path+".value", "built-in must be DIRECT or REJECT")
				}
			case MemberTag:
				if !NodeTagPattern.MatchString(m.Tag) {
					add(path+".tag", "tag must be 1-32 lowercase letters, digits, '.', '-' or '_'")
				}
			case MemberNode:
				if !uuidPattern.MatchString(m.NodeID) {
					add(path+".node_id", "node_id must be a node UUID")
				}
			case MemberAll:
			default:
				add(path+".kind", "kind must be group, builtin, tag, node or all")
			}
		}
	}
	if cycle := groupCycle(p.Groups, groups); cycle != "" {
		add("groups", "groups reference each other in a cycle: %s", cycle)
	}

	providers := map[string]RoutingProvider{}
	for i, pr := range p.Providers {
		path := fmt.Sprintf("providers[%d]", i)
		if !routingKeyPattern.MatchString(pr.Key) {
			add(path+".key", "key must match ^[a-z0-9_-]{1,32}$")
		} else if _, dup := providers[pr.Key]; dup {
			add(path+".key", "duplicate rule set key %q", pr.Key)
		} else {
			providers[pr.Key] = pr
		}
		if pr.Behavior != "domain" && pr.Behavior != "ipcidr" && pr.Behavior != "classical" {
			add(path+".behavior", "behavior must be domain, ipcidr or classical")
		}
		if pr.Interval != 0 && (pr.Interval < 3600 || pr.Interval > 604800) {
			add(path+".interval", "interval must be 3600-604800 seconds")
		}
		switch pr.Type {
		case "http":
			if !strings.HasPrefix(pr.URL, "https://") || strings.ContainsAny(pr.URL, " \n\r\t") {
				add(path+".url", "URL must start with https:// and contain no spaces")
			}
			switch pr.Format {
			case "mrs":
				if pr.Behavior == "classical" {
					add(path+".format", "mrs supports only domain and ipcidr behaviors")
				}
			case "yaml", "text":
			default:
				add(path+".format", "format must be mrs, yaml or text")
			}
		case "inline":
			if len(pr.Payload) == 0 || len(pr.Payload) > maxInlinePayload {
				add(path+".payload", "inline rule sets need 1-%d entries", maxInlinePayload)
			}
			for j, entry := range pr.Payload {
				if msg := payloadProblem(pr.Behavior, entry); msg != "" {
					add(fmt.Sprintf("%s.payload[%d]", path, j), "%s", msg)
				}
			}
		default:
			add(path+".type", "type must be http or inline")
		}
	}

	for i, r := range p.Rules {
		path := fmt.Sprintf("rules[%d]", i)
		if !ruleTypes[r.Type] {
			add(path+".type", "unsupported rule type %q", r.Type)
		} else if msg := ruleValueProblem(r.Type, r.Value, providers); msg != "" {
			add(path+".value", "%s", msg)
		}
		if !targetExists(r.Target, groups) {
			add(path+".target", "unknown target %q", r.Target)
		}
		if r.NoResolve && !noResolveTypes[r.Type] {
			add(path+".no_resolve", "no-resolve applies only to RULE-SET, IP-CIDR and IP-CIDR6")
		}
	}

	if _, ok := groups[p.Final]; !ok {
		add("final", "final must name an existing group")
	}
	validateSettings(p.EffectiveSettings(), add)
	return errs
}

// NormalizeNodeTags lowercases, trims, de-duplicates and validates node tags.
// When ok is false, bad names the first offending input.
func NormalizeNodeTags(raw []string) (tags []string, bad string, ok bool) {
	tags = make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, t := range raw {
		tag := strings.ToLower(strings.TrimSpace(t))
		if tag == "" || seen[tag] {
			continue
		}
		if !NodeTagPattern.MatchString(tag) {
			return nil, t, false
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	if len(tags) > maxNodeTags {
		return nil, fmt.Sprintf("%d tags (max %d)", len(tags), maxNodeTags), false
	}
	return tags, "", true
}

func groupNameProblem(name string) string {
	switch {
	case strings.TrimSpace(name) == "" || len([]rune(name)) > 64:
		return "name must be 1-64 characters"
	case name != strings.TrimSpace(name):
		return "name must not start or end with spaces"
	case strings.ContainsAny(name, ",()\n\r"):
		return "name must not contain commas, parentheses or line breaks"
	case reservedNames[strings.ToUpper(name)]:
		return "name is reserved by Clash"
	}
	return ""
}

func plainValueProblem(v string) string {
	switch {
	case v == "" || len(v) > 253:
		return "value must be 1-253 characters"
	case v != strings.TrimSpace(v):
		return "value must not start or end with spaces"
	case strings.ContainsAny(v, ",\n\r"):
		return "value must not contain commas or line breaks"
	}
	return ""
}

func payloadProblem(behavior, entry string) string {
	if behavior == "classical" {
		if entry == "" || len(entry) > 512 || strings.ContainsAny(entry, "\n\r") {
			return "entry must be 1-512 characters on one line"
		}
		return ""
	}
	if msg := plainValueProblem(entry); msg != "" {
		return msg
	}
	if behavior == "ipcidr" {
		if _, err := netip.ParsePrefix(entry); err != nil {
			return "entry must be an IP prefix like 1.2.3.0/24"
		}
	}
	return ""
}

func ruleValueProblem(ruleType, v string, providers map[string]RoutingProvider) string {
	if msg := plainValueProblem(v); msg != "" {
		return msg
	}
	switch ruleType {
	case "RULE-SET":
		if _, ok := providers[v]; !ok {
			return fmt.Sprintf("unknown rule set %q", v)
		}
	case "IP-CIDR", "IP-CIDR6":
		prefix, err := netip.ParsePrefix(v)
		if err != nil || (ruleType == "IP-CIDR") != prefix.Addr().Is4() {
			if ruleType == "IP-CIDR" {
				return "value must be an IPv4 prefix like 1.2.3.0/24"
			}
			return "value must be an IPv6 prefix like 2001:db8::/32"
		}
	case "DST-PORT":
		if !validPortSpec(v) {
			return "value must be a port or range like 443 or 8000-8100"
		}
	}
	return ""
}

func validPortSpec(v string) bool {
	lo, hi, isRange := strings.Cut(v, "-")
	a, err := strconv.Atoi(lo)
	if err != nil || a < 1 || a > 65535 {
		return false
	}
	if !isRange {
		return true
	}
	b, err := strconv.Atoi(hi)
	return err == nil && b >= a && b <= 65535
}

func targetExists(target string, groups map[string]RoutingGroup) bool {
	if target == "DIRECT" || target == "REJECT" {
		return true
	}
	_, ok := groups[target]
	return ok
}

// groupCycle returns a "a -> b -> a" description of the first reference cycle,
// or "" when group references form a DAG. Clash rejects cyclic groups at load.
func groupCycle(order []RoutingGroup, groups map[string]RoutingGroup) string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(key string) string
	visit = func(key string) string {
		state[key] = visiting
		stack = append(stack, key)
		for _, m := range groups[key].Members {
			if m.Kind != MemberGroup {
				continue
			}
			if _, ok := groups[m.Ref]; !ok || m.Ref == key {
				continue
			}
			switch state[m.Ref] {
			case visiting:
				start := 0
				for i, k := range stack {
					if k == m.Ref {
						start = i
					}
				}
				return strings.Join(append(append([]string{}, stack[start:]...), m.Ref), " -> ")
			case unvisited:
				if c := visit(m.Ref); c != "" {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[key] = done
		return ""
	}
	for _, g := range order {
		if _, ok := groups[g.Key]; ok && state[g.Key] == unvisited {
			if c := visit(g.Key); c != "" {
				return c
			}
		}
	}
	return ""
}
