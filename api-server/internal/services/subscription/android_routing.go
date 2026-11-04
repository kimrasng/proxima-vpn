package subscription

import (
	"encoding/json"
	"fmt"
)

type androidRoutingDocument struct {
	Version     int                  `json:"version"`
	Rules       []androidRoutingRule `json:"rules"`
	AutoTestURL string               `json:"autoTestUrl"`
}

type androidRoutingRule struct {
	Target     string   `json:"target"`
	Domain     []string `json:"domain,omitempty"`
	IP         []string `json:"ip,omitempty"`
	Network    string   `json:"network,omitempty"`
	Port       string   `json:"port,omitempty"`
	Match      bool     `json:"match,omitempty"`
	Policy     string   `json:"policy,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

// AndroidRoutingError identifies a profile or expanded-provider value that
// cannot be represented faithfully by the Android metadata contract.
type AndroidRoutingError struct {
	Path    string
	Problem string
}

func (e *AndroidRoutingError) Error() string {
	return fmt.Sprintf("android routing %s: %s", e.Path, e.Problem)
}

func androidRoutingProblem(path, problem string) error {
	return &AndroidRoutingError{Path: path, Problem: problem}
}

// GenerateAndroidRouting returns version-1 JSON, not base64 or VLESS links.
// The caller embeds base64(JSON) on a separate # proxima-routing-rules: line
// beside VLESS links whose decoded names equal NodeInfo.Name exactly.
//
// providers contains fully downloaded/decoded HTTP provider payloads keyed by
// RoutingProvider.Key; inline payloads always come from the profile. This pure
// function performs no fetching and never substitutes geoip/geosite references.
// Missing or unrepresentable data returns an error, never partial metadata.
//
// Group selection uses the first live member of select groups (the Clash
// default, not a user's persisted selection). Android supports only leastPing
// proxy policies: flat fallback groups intentionally use leastPing rather than
// Clash's ordered failover. Nested automatic policies that would lose group
// boundaries return errors instead of merging independent selections.
// NoResolve has no wire representation: IP rules match the destination IP the
// Android client supplies, without requesting additional DNS resolution here.
func GenerateAndroidRouting(profile RoutingProfile, providers map[string][]string, nodes []NodeInfo) ([]byte, error) {
	if err := checkAndroidRoutingBudget(profile, providers, nodes); err != nil {
		return nil, err
	}
	if errs := validateMobileRoutingProfile(profile); len(errs) != 0 {
		return nil, androidRoutingProblem(errs[0].Path, errs[0].Message)
	}
	resolver, err := newAndroidPolicyResolver(profile, nodes)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]RoutingProvider, len(profile.Providers))
	for _, provider := range profile.Providers {
		byKey[provider.Key] = provider
	}
	doc := androidRoutingDocument{Version: 1, Rules: []androidRoutingRule{}}
	if profile.BlockQUIC {
		doc.Rules = append(doc.Rules, androidRoutingRule{Target: "block", Network: "udp", Port: "443"})
	}
	for i, rule := range profile.Rules {
		path := fmt.Sprintf("rules[%d]", i)
		var expanded []androidRoutingRule
		if rule.Type == "RULE-SET" {
			provider := byKey[rule.Value]
			payload := provider.Payload
			if provider.Type == "http" {
				var found bool
				payload, found = providers[provider.Key]
				if !found {
					return nil, androidRoutingProblem(path, "missing expanded provider "+provider.Key)
				}
			}
			expanded, err = androidProviderRules(provider.Behavior, payload, rule.Target, resolver)
		} else {
			var match androidRoutingRule
			match, err = androidRuleMatch(rule.Type, rule.Value)
			if err == nil {
				var policy androidPolicy
				policy, err = resolver.target(rule.Target)
				match.Target, match.Policy, match.Candidates = policy.target, policy.key, policy.candidates
			}
			expanded = []androidRoutingRule{match}
		}
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", path, rule.Value, err)
		}
		doc.Rules = append(doc.Rules, expanded...)
	}
	final, err := resolver.target(profile.Final)
	if err != nil {
		return nil, fmt.Errorf("final: %w", err)
	}
	doc.Rules = append(doc.Rules, androidRoutingRule{
		Target: final.target, Policy: final.key, Candidates: final.candidates, Match: true,
	})
	doc.AutoTestURL = orDefault(resolver.healthURL, defaultHealthURL)
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal android routing: %w", err)
	}
	return data, nil
}
