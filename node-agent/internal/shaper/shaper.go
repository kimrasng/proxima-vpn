// Package shaper applies per-speed-tier bandwidth limits using tc (traffic
// control). Xray cannot rate-limit individual users, so the API server places
// speed-limited clients on dedicated VLESS Reality inbounds (one port per Mbps
// tier, see pkg/speedtier). This package reads those inbounds from the Xray
// config and installs an HTB qdisc that caps egress (download) throughput on
// each tier's port. Unlimited traffic passes through an unshaped default class.
package shaper

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/proximavpn/proxima-vpn/pkg/speedtier"
)

// Tier is a single speed-limited inbound: all traffic on Port is capped to Mbps.
type Tier struct {
	Port int
	Mbps int
}

// runCmd runs a command; overridable in tests.
var runCmd = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// TiersFromConfig extracts the speed-limited inbounds from an Xray config JSON.
func TiersFromConfig(config []byte) []Tier {
	var parsed struct {
		Inbounds []struct {
			Port int    `json:"port"`
			Tag  string `json:"tag"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return nil
	}

	var tiers []Tier
	for _, ib := range parsed.Inbounds {
		if mbps, ok := speedtier.ParseLimitTag(ib.Tag); ok && ib.Port > 0 {
			tiers = append(tiers, Tier{Port: ib.Port, Mbps: mbps})
		}
	}
	sort.Slice(tiers, func(i, j int) bool { return tiers[i].Port < tiers[j].Port })
	return tiers
}

// Apply installs egress HTB shaping for the given tiers on iface. If iface is
// empty the default route interface is used. An empty tier list removes any
// existing shaping. tc requires root; callers typically log errors and continue.
func Apply(iface string, tiers []Tier) error {
	if iface == "" {
		iface = defaultInterface()
	}
	if iface == "" {
		return fmt.Errorf("could not determine default network interface")
	}

	// Reset any existing root qdisc first (ignore "nothing to delete" errors)
	// so re-applying is idempotent.
	_ = runCmd("tc", "qdisc", "del", "dev", iface, "root")

	if len(tiers) == 0 {
		return nil
	}

	// Root HTB with a high-rate default class (30) for unlimited traffic.
	if err := runCmd("tc", "qdisc", "add", "dev", iface, "root", "handle", "1:", "htb", "default", "30"); err != nil {
		return fmt.Errorf("add root qdisc: %w", err)
	}
	if err := runCmd("tc", "class", "add", "dev", iface, "parent", "1:", "classid", "1:1", "htb", "rate", "10000mbit"); err != nil {
		return fmt.Errorf("add root class: %w", err)
	}
	if err := runCmd("tc", "class", "add", "dev", iface, "parent", "1:1", "classid", "1:30", "htb", "rate", "10000mbit", "ceil", "10000mbit"); err != nil {
		return fmt.Errorf("add default class: %w", err)
	}

	for i, t := range tiers {
		classID := fmt.Sprintf("1:%d", 100+i)
		rate := fmt.Sprintf("%dmbit", t.Mbps)
		if err := runCmd("tc", "class", "add", "dev", iface, "parent", "1:1", "classid", classID, "htb", "rate", rate, "ceil", rate); err != nil {
			return fmt.Errorf("add tier class for port %d: %w", t.Port, err)
		}
		// Match egress packets whose source port is the tier port (server ->
		// client download) and direct them into the capped class.
		if err := runCmd("tc", "filter", "add", "dev", iface, "protocol", "ip", "parent", "1:0", "prio", "1",
			"u32", "match", "ip", "sport", fmt.Sprintf("%d", t.Port), "0xffff", "flowid", classID); err != nil {
			return fmt.Errorf("add tier filter for port %d: %w", t.Port, err)
		}
	}

	return nil
}

// defaultInterface returns the interface used by the default route.
func defaultInterface() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}
