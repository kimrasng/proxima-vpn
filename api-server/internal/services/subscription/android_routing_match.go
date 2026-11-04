package subscription

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Each provider is expanded at its original position. Adjacent entries with
// the same policy and match dimension share an OR array, but domain and IP conditions are
// never combined: Xray would interpret those dimensions as AND.
func androidProviderRules(behavior string, payload []string, inheritedTarget string, resolver *androidPolicyResolver) ([]androidRoutingRule, error) {
	var rules []androidRoutingRule
	for i, raw := range payload {
		entry := strings.TrimSpace(raw)
		if entry == "" || strings.HasPrefix(entry, "#") || strings.HasPrefix(entry, "//") {
			continue
		}
		kind, value := "", entry
		explicitTarget := ""
		switch behavior {
		case "domain":
			kind = "DOMAIN"
			if strings.HasPrefix(value, "+.") {
				kind, value = "DOMAIN-SUFFIX", strings.TrimPrefix(value, "+.")
			}
		case "ipcidr":
			kind = "IP-CIDR"
			if strings.Contains(value, ":") {
				kind = "IP-CIDR6"
			}
		case "classical":
			parts := strings.SplitN(entry, ",", 5)
			if len(parts) < 2 || len(parts) > 4 {
				return nil, androidRoutingProblem(fmt.Sprintf("payload[%d]", i), "classical entries must be TYPE,value[,target][,no-resolve]")
			}
			kind, value = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			noResolve := strings.TrimSpace(parts[len(parts)-1]) == "no-resolve" && len(parts) > 2
			if noResolve {
				parts = parts[:len(parts)-1]
			}
			if len(parts) > 3 || (len(parts) == 3 && strings.TrimSpace(parts[2]) == "") {
				return nil, androidRoutingProblem(fmt.Sprintf("payload[%d]", i), "invalid classical target or trailing option")
			}
			if len(parts) == 3 {
				explicitTarget = strings.TrimSpace(parts[2])
			}
			if noResolve && kind != "IP-CIDR" && kind != "IP-CIDR6" {
				return nil, androidRoutingProblem(fmt.Sprintf("payload[%d]", i), "no-resolve is supported only for IP prefixes")
			}
		default:
			return nil, androidRoutingProblem("behavior", "unsupported provider behavior "+behavior)
		}
		rule, err := androidRuleMatch(kind, value)
		if err != nil {
			return nil, fmt.Errorf("payload[%d]: %w", i, err)
		}
		var policy androidPolicy
		if explicitTarget != "" {
			policy, err = resolver.explicitTarget(explicitTarget)
		} else {
			policy, err = resolver.target(inheritedTarget)
		}
		if err != nil {
			return nil, fmt.Errorf("payload[%d]: %w", i, err)
		}
		rule.Target, rule.Policy, rule.Candidates = policy.target, policy.key, policy.candidates
		if len(rules) > 0 {
			previous := &rules[len(rules)-1]
			samePolicy := previous.Target == rule.Target && previous.Policy == rule.Policy && slices.Equal(previous.Candidates, rule.Candidates)
			switch {
			case samePolicy && len(previous.Domain) > 0 && len(rule.Domain) > 0:
				previous.Domain = append(previous.Domain, rule.Domain...)
				continue
			case samePolicy && len(previous.IP) > 0 && len(rule.IP) > 0:
				previous.IP = append(previous.IP, rule.IP...)
				continue
			}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func androidRuleMatch(kind, value string) (androidRoutingRule, error) {
	var rule androidRoutingRule
	if value == "" || strings.ContainsAny(value, "\r\n\t ,") {
		return rule, androidRoutingProblem("value", "empty value or separators in match")
	}
	switch kind {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD":
		// Do not reinterpret Clash wildcards or opaque geo tokens as exact
		// domains. In particular *.example is not domain:example (apex).
		if strings.ContainsAny(value, "*+:/\\") || strings.HasPrefix(value, ".") {
			return rule, androidRoutingProblem("domain", "wildcards, leading-dot domains and opaque geo references are not supported")
		}
		// Xray's unprefixed domain string is a substring matcher, including
		// older Android cores that predate the explicit keyword: prefix.
		prefix := map[string]string{"DOMAIN": "full:", "DOMAIN-SUFFIX": "domain:", "DOMAIN-KEYWORD": ""}[kind]
		rule.Domain = []string{prefix + value}
	case "IP-CIDR", "IP-CIDR6":
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().Is4() != (kind == "IP-CIDR") {
			return rule, androidRoutingProblem("ip", "expected a concrete IP prefix of the declared address family")
		}
		rule.IP = []string{prefix.Masked().String()}
	case "DST-PORT":
		if !validPortSpec(value) {
			return rule, androidRoutingProblem("port", "expected a port or inclusive range in 1-65535")
		}
		rule.Port = value
	default:
		return rule, androidRoutingProblem("type", "unsupported rule type "+kind)
	}
	return rule, nil
}
