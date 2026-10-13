package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

// liveTopology is the part of a materialized Xray config that the agent keeps
// in sync over gRPC: the per-device SOCKS egress outbounds and the complete,
// ordered routing rule list (device rules plus the structural ones around
// them, such as the controlled-inbound deny fallback).
type liveTopology struct {
	outbounds map[string]xray.SOCKSOutbound
	rules     []xray.RoutingRule
}

type rawRule struct {
	Type        string   `json:"type"`
	RuleTag     string   `json:"ruleTag"`
	OutboundTag string   `json:"outboundTag"`
	User        []string `json:"user"`
	InboundTag  []string `json:"inboundTag"`
	Source      []string `json:"source"`
}

// parseLiveTopology extracts the live-managed topology from a materialized
// (runtime) config, which carries the node-local SOCKS passwords. Rules the
// agent cannot express over gRPC make the topology unusable for live edits;
// the caller then falls back to a restart.
func parseLiveTopology(runtime []byte) (liveTopology, error) {
	var cfg struct {
		Outbounds []struct {
			Tag      string          `json:"tag"`
			Protocol string          `json:"protocol"`
			Settings json.RawMessage `json:"settings"`
		} `json:"outbounds"`
		Routing struct {
			Rules     []json.RawMessage `json:"rules"`
			Balancers []json.RawMessage `json:"balancers"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(runtime, &cfg); err != nil {
		return liveTopology{}, fmt.Errorf("decode runtime config: %w", err)
	}
	// Replacing the rule list (AddRule with shouldAppend=false) also resets
	// the router's balancers, which a live edit cannot rebuild.
	if len(cfg.Routing.Balancers) > 0 {
		return liveTopology{}, fmt.Errorf("routing balancers cannot be preserved by a live rule swap")
	}
	topology := liveTopology{outbounds: make(map[string]xray.SOCKSOutbound)}
	for _, outbound := range cfg.Outbounds {
		if !devicebandwidth.IsDeviceOutboundTag(outbound.Tag) {
			continue
		}
		var settings struct {
			Servers []struct {
				Address string `json:"address"`
				Port    uint32 `json:"port"`
				Users   []struct {
					User string `json:"user"`
					Pass string `json:"pass"`
				} `json:"users"`
			} `json:"servers"`
		}
		if outbound.Protocol != "socks" || json.Unmarshal(outbound.Settings, &settings) != nil ||
			len(settings.Servers) != 1 || len(settings.Servers[0].Users) != 1 {
			return liveTopology{}, fmt.Errorf("device outbound %q is not a single-user SOCKS outbound", outbound.Tag)
		}
		server := settings.Servers[0]
		if server.Users[0].Pass == "" || server.Users[0].Pass == deviceEgressPlaceholder {
			return liveTopology{}, fmt.Errorf("device outbound %q has no materialized credential", outbound.Tag)
		}
		topology.outbounds[outbound.Tag] = xray.SOCKSOutbound{
			Tag:      outbound.Tag,
			Address:  server.Address,
			Port:     server.Port,
			Username: server.Users[0].User,
			Password: server.Users[0].Pass,
		}
	}
	for i, raw := range cfg.Routing.Rules {
		var fields map[string]json.RawMessage
		var rule rawRule
		if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &rule) != nil {
			return liveTopology{}, fmt.Errorf("decode routing rule %d", i)
		}
		for key := range fields {
			switch key {
			case "type", "ruleTag", "outboundTag", "user", "inboundTag", "source":
			default:
				return liveTopology{}, fmt.Errorf("routing rule %d uses %q, which live edits cannot express", i, key)
			}
		}
		if rule.Type != "" && rule.Type != "field" || rule.OutboundTag == "" {
			return liveTopology{}, fmt.Errorf("routing rule %d is not a field rule with an outbound", i)
		}
		for _, source := range rule.Source {
			if _, _, err := net.ParseCIDR(source); err != nil {
				return liveTopology{}, fmt.Errorf("routing rule %d has invalid source CIDR", i)
			}
		}
		topology.rules = append(topology.rules, xray.RoutingRule{
			RuleTag:     rule.RuleTag,
			OutboundTag: rule.OutboundTag,
			UserEmail:   rule.User,
			InboundTag:  rule.InboundTag,
			SourceCIDRs: rule.Source,
		})
	}
	return topology, nil
}

// sameRules reports whether two rule lists are identical in order and content.
func sameRules(a, b []xray.RoutingRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].RuleTag != b[i].RuleTag || a[i].OutboundTag != b[i].OutboundTag ||
			strings.Join(a[i].UserEmail, "\x00") != strings.Join(b[i].UserEmail, "\x00") ||
			strings.Join(a[i].InboundTag, "\x00") != strings.Join(b[i].InboundTag, "\x00") ||
			strings.Join(a[i].SourceCIDRs, "\x00") != strings.Join(b[i].SourceCIDRs, "\x00") {
			return false
		}
	}
	return true
}

// topologyClient is the slice of the Xray gRPC client syncDeviceTopology uses.
type topologyClient interface {
	AddSOCKSOutbound(context.Context, xray.SOCKSOutbound) error
	RemoveOutbound(context.Context, string) error
	ReplaceRoutingRules(context.Context, []xray.RoutingRule) error
}

// syncDeviceTopology moves the running Xray from prev's device topology to
// next's: it adds new device outbounds, swaps in the complete ordered rule
// list atomically, then removes outbounds no rule routes to any more. A rule
// may only name an outbound that exists, and an outbound goes away only once
// nothing routes to it. Any failure returns false so the caller restarts Xray
// onto next, which is always safe.
func syncDeviceTopology(ctx context.Context, client topologyClient, prev, next []byte) bool {
	if client == nil {
		return false
	}
	before, err := parseLiveTopology(prev)
	if err != nil {
		log.Printf("live topology: current config: %v", err)
		return false
	}
	after, err := parseLiveTopology(next)
	if err != nil {
		log.Printf("live topology: new config: %v", err)
		return false
	}
	for _, tag := range sortedKeys(after.outbounds) {
		outbound := after.outbounds[tag]
		if existing, ok := before.outbounds[tag]; ok {
			if existing == outbound {
				continue
			}
			// Xray rejects a duplicate tag, and replacing it live would race
			// the rule that routes to it.
			log.Printf("live topology: outbound %s changed in place", tag)
			return false
		}
		if err := client.AddSOCKSOutbound(ctx, outbound); err != nil {
			log.Printf("live topology: %v", err)
			return false
		}
	}
	if !sameRules(before.rules, after.rules) {
		if err := client.ReplaceRoutingRules(ctx, after.rules); err != nil {
			log.Printf("live topology: %v", err)
			return false
		}
	}
	for _, tag := range sortedKeys(before.outbounds) {
		if _, keep := after.outbounds[tag]; keep {
			continue
		}
		if err := client.RemoveOutbound(ctx, tag); err != nil {
			log.Printf("live topology: %v", err)
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]xray.SOCKSOutbound) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
