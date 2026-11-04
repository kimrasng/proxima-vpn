package subscription

import (
	"fmt"
	"slices"
	"strings"
)

type androidPolicy struct {
	target     string
	key        string
	candidates []string
}

type androidPolicyResolver struct {
	profile   RoutingProfile
	groups    map[string]RoutingGroup
	members   map[string][]resolvedMember
	resolved  map[string]androidPolicy
	healthURL string
}

func newAndroidPolicyResolver(profile RoutingProfile, nodes []NodeInfo) (*androidPolicyResolver, error) {
	var kept []NodeInfo
	var names []string
	seen := map[string]bool{}
	for i, node := range nodes {
		// Android's sibling link list contains VLESS only. Do not create
		// candidates for protocols that cannot appear in that list.
		if node.Protocol != "vless_reality" {
			continue
		}
		if strings.TrimSpace(node.Name) == "" || seen[node.Name] || node.Name == "DIRECT" || node.Name == "REJECT" {
			return nil, androidRoutingProblem(fmt.Sprintf("nodes[%d].name", i), "VLESS candidate names must be nonempty, unique and not DIRECT/REJECT")
		}
		seen[node.Name] = true
		kept = append(kept, node)
		names = append(names, node.Name)
	}
	groups := make(map[string]RoutingGroup, len(profile.Groups))
	for _, group := range profile.Groups {
		groups[group.Key] = group
	}
	return &androidPolicyResolver{
		profile: profile, groups: groups,
		members:  resolveGroupMembers(profile, names, kept),
		resolved: map[string]androidPolicy{},
	}, nil
}

func (r *androidPolicyResolver) target(key string) (androidPolicy, error) {
	switch key {
	case "DIRECT":
		return androidPolicy{target: "direct"}, nil
	case "REJECT":
		return androidPolicy{target: "block"}, nil
	}
	if len(r.members[key]) == 0 {
		return androidPolicy{}, androidRoutingProblem("group "+key, "routing target has no compatible candidates")
	}
	return r.group(key)
}

// Classical targets may use either stable keys or display names. Reject a
// name/key collision instead of silently routing to either possible group.
func (r *androidPolicyResolver) explicitTarget(target string) (androidPolicy, error) {
	if target == "DIRECT" || target == "REJECT" {
		return r.target(target)
	}
	key := ""
	for _, group := range r.profile.Groups {
		if group.Key == target || group.Name == target {
			if key != "" && key != group.Key {
				return androidPolicy{}, androidRoutingProblem("target "+target, "ambiguous group key/name")
			}
			key = group.Key
		}
	}
	if key == "" {
		return androidPolicy{}, androidRoutingProblem("target "+target, "unknown explicit group key/name")
	}
	return r.target(key)
}

func (r *androidPolicyResolver) member(member resolvedMember, key string) (androidPolicy, error) {
	if member.group != "" {
		return r.group(member.group)
	}
	switch member.proxy {
	case "DIRECT", "REJECT":
		return r.target(member.proxy)
	default:
		return androidPolicy{target: "proxy", key: key, candidates: []string{member.proxy}}, nil
	}
}

func (r *androidPolicyResolver) group(key string) (androidPolicy, error) {
	if policy, ok := r.resolved[key]; ok {
		return policy, nil
	}
	group := r.groups[key]
	members := r.members[key]
	path := "group " + key
	if group.Type == "select" || len(members) == 1 {
		policy, err := r.member(members[0], key)
		if err != nil {
			return androidPolicy{}, err
		}
		if group.Type != "select" && policy.target == "proxy" && len(policy.candidates) == 1 {
			if err := r.useHealthURL(group); err != nil {
				return androidPolicy{}, err
			}
		}
		r.resolved[key] = policy
		return policy, nil
	}
	// Android intentionally uses leastPing for both url-test and fallback.
	// Keep the enclosing select branch; do not merge its lower-priority groups.
	policy := androidPolicy{target: "proxy", key: key}
	for _, member := range members {
		child, err := r.member(member, key)
		if err != nil {
			return androidPolicy{}, err
		}
		if child.target != "proxy" {
			return androidPolicy{}, androidRoutingProblem(path, "automatic selection containing DIRECT or REJECT cannot be represented by proxy candidates")
		}
		if len(child.candidates) != 1 {
			return androidPolicy{}, androidRoutingProblem(path, "nested multi-candidate selection cannot be represented by one leastPing balancer")
		}
		if !slices.Contains(policy.candidates, child.candidates[0]) {
			policy.candidates = append(policy.candidates, child.candidates[0])
		}
	}
	if err := r.useHealthURL(group); err != nil {
		return androidPolicy{}, err
	}
	r.resolved[key] = policy
	return policy, nil
}

func (r *androidPolicyResolver) useHealthURL(group RoutingGroup) error {
	url := orDefault(group.URL, defaultHealthURL)
	if r.healthURL != "" && r.healthURL != url {
		return androidRoutingProblem("group "+group.Key, "different active health-check URLs cannot be represented by one autoTestUrl")
	}
	r.healthURL = url
	return nil
}
