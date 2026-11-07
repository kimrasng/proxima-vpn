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
			parts, err := androidRuleParts(entry)
			if err != nil {
				return nil, fmt.Errorf("payload[%d]: %w", i, err)
			}
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
			case samePolicy && len(previous.Domain) > 0 && len(rule.Domain) > 0 && len(previous.IP) == 0 && len(rule.IP) == 0 && previous.Network == "" && rule.Network == "" && previous.Port == "" && rule.Port == "":
				previous.Domain = append(previous.Domain, rule.Domain...)
				continue
			case samePolicy && len(previous.IP) > 0 && len(rule.IP) > 0 && len(previous.Domain) == 0 && len(rule.Domain) == 0 && previous.Network == "" && rule.Network == "" && previous.Port == "" && rule.Port == "":
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
	if kind == "AND" {
		return androidAndMatch(value)
	}
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
	case "NETWORK":
		switch strings.ToLower(value) {
		case "tcp", "udp":
			rule.Network = strings.ToLower(value)
		default:
			return rule, androidRoutingProblem("network", "expected TCP or UDP")
		}
	default:
		return rule, androidRoutingProblem("type", "unsupported rule type "+kind)
	}
	return rule, nil
}

// Split only at the current expression level, keeping AND operands intact.
func androidRuleParts(value string) ([]string, error) {
	var parts []string
	depth, start := 0, 0
	for i, char := range value {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, androidRoutingProblem("value", "unbalanced parentheses")
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(value[start:i]))
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, androidRoutingProblem("value", "unbalanced parentheses")
	}
	return append(parts, strings.TrimSpace(value[start:])), nil
}

func androidAndMatch(value string) (androidRoutingRule, error) {
	var rule androidRoutingRule
	if !strings.HasPrefix(value, "(") || !strings.HasSuffix(value, ")") {
		return rule, androidRoutingProblem("AND", "expected parenthesized conditions")
	}
	operands, err := androidRuleParts(value[1 : len(value)-1])
	if err != nil {
		return rule, err
	}
	if len(operands) < 2 {
		return rule, androidRoutingProblem("AND", "expected at least two conditions")
	}
	for _, operand := range operands {
		if !strings.HasPrefix(operand, "(") || !strings.HasSuffix(operand, ")") {
			return rule, androidRoutingProblem("AND", "expected parenthesized condition")
		}
		parts, err := androidRuleParts(operand[1 : len(operand)-1])
		if err != nil {
			return rule, err
		}
		if len(parts) != 2 || parts[0] == "AND" {
			return rule, androidRoutingProblem("AND", "expected a simple TYPE,value condition")
		}
		match, err := androidRuleMatch(parts[0], parts[1])
		if err != nil {
			return rule, err
		}
		// Arrays are OR within a dimension, so repeated dimensions cannot
		// represent a conjunction faithfully in the Android wire contract.
		if len(match.Domain) > 0 {
			if len(rule.Domain) > 0 {
				return rule, androidRoutingProblem("AND", "repeated domain conditions")
			}
			rule.Domain = match.Domain
		}
		if len(match.IP) > 0 {
			if len(rule.IP) > 0 {
				return rule, androidRoutingProblem("AND", "repeated IP conditions")
			}
			rule.IP = match.IP
		}
		if match.Network != "" {
			if rule.Network != "" {
				return rule, androidRoutingProblem("AND", "repeated network conditions")
			}
			rule.Network = match.Network
		}
		if match.Port != "" {
			if rule.Port != "" {
				return rule, androidRoutingProblem("AND", "repeated port conditions")
			}
			rule.Port = match.Port
		}
	}
	return rule, nil
}
