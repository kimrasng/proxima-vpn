// Package nodeprov defines the provisioning choices an operator makes in the
// panel before a node install command is issued: the node's role, the target OS
// family, and which firewall ports to open.
//
// The speed-tier range is not a choice. Speed-limited plans place clients on
// dedicated inbounds at speedtier.PortBase+Mbps, so that range must be open on
// every node or those plans fail with no visible cause. Presets therefore only
// select the service port and any extras; ResolvePorts always appends the tier
// range.
//
// A relay's chain entry ports are NOT opened here. See RelayRange.
package nodeprov

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/proximavpn/proxima-vpn/pkg/speedtier"
)

type OSFamily string

const (
	OSDebian OSFamily = "debian"
	OSRHEL   OSFamily = "rhel"
	OSAlpine OSFamily = "alpine"
)

func (o OSFamily) Valid() bool {
	switch o {
	case OSDebian, OSRHEL, OSAlpine:
		return true
	}
	return false
}

func OSFamilies() []OSFamily {
	return []OSFamily{OSDebian, OSRHEL, OSAlpine}
}

// Role is what a node does in the data path. An exit terminates client tunnels
// and reaches the internet; a relay only forwards opaque bytes to an exit, and
// RoleBoth does both.
type Role string

const (
	RoleExit    Role = "exit"
	RoleRelay   Role = "relay"
	RoleBoth    Role = "both"
	DefaultRole      = RoleExit
)

func (r Role) Valid() bool {
	switch r {
	case RoleExit, RoleRelay, RoleBoth:
		return true
	}
	return false
}

// Forwards reports whether a node with this role hosts relay entry ports.
func (r Role) Forwards() bool {
	return r == RoleRelay || r == RoleBoth
}

// Exits reports whether a node with this role terminates client tunnels.
func (r Role) Exits() bool {
	return r == RoleExit || r == RoleBoth
}

func Roles() []Role {
	return []Role{RoleExit, RoleRelay, RoleBoth}
}

type FirewallPreset string

const (
	PresetStandard     FirewallPreset = "standard"
	PresetWebAlt       FirewallPreset = "web_alt"
	PresetHighPort     FirewallPreset = "high_port"
	PresetCustom       FirewallPreset = "custom"
	DefaultPreset                     = PresetStandard
	DefaultServicePort                = 443
)

func (p FirewallPreset) Valid() bool {
	switch p {
	case PresetStandard, PresetWebAlt, PresetHighPort, PresetCustom:
		return true
	}
	return false
}

func Presets() []FirewallPreset {
	return []FirewallPreset{PresetStandard, PresetWebAlt, PresetHighPort, PresetCustom}
}

// PortSpec is a single port or an inclusive range. End equals Start for a
// single port.
type PortSpec struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (s PortSpec) String() string {
	if s.Start == s.End {
		return strconv.Itoa(s.Start)
	}
	return fmt.Sprintf("%d-%d", s.Start, s.End)
}

// TierRange is the inbound range reserved for speed-limited plans.
func TierRange() PortSpec {
	return PortSpec{Start: speedtier.PortBase + 1, End: speedtier.PortBase + speedtier.MaxMbps}
}

const (
	// RelayPortBase is the port below the first relay chain entry port. The
	// range starts above the speed-tier range (speedtier.PortBase+MaxMbps =
	// 22000) so a chain entry port can never collide with a tier inbound.
	RelayPortBase = 23000
	// MaxRelayChains bounds how many chain entry ports one relay can host.
	MaxRelayChains = 1000
)

// RelayRange is the range chain entry ports are auto-allocated from. It is
// deliberately absent from ResolvePorts: a relayed packet is redirected in
// PREROUTING and leaves through FORWARD, so it never reaches INPUT and opening
// these ports there would protect nothing. What a relay needs instead is IP
// forwarding and a FORWARD policy, which the node agent owns.
func RelayRange() PortSpec {
	return PortSpec{Start: RelayPortBase + 1, End: RelayPortBase + MaxRelayChains}
}

// presetExtras are the ports a preset opens in addition to the service port and
// the tier range.
func presetExtras(p FirewallPreset) []PortSpec {
	if p == PresetWebAlt {
		return []PortSpec{{Start: 80, End: 80}}
	}
	return nil
}

// ResolvePorts returns the full set of ports to open, given a node role, a
// preset, the node service port, and the extra specs supplied for the custom
// preset. The tier range is always included; the relay range is included for
// roles that forward. Results are normalised: sorted, with overlapping and
// adjacent specs merged, so the install script never receives redundant rules.
func ResolvePorts(role Role, preset FirewallPreset, servicePort int, custom []PortSpec) ([]PortSpec, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("unknown node role %q", role)
	}
	if !preset.Valid() {
		return nil, fmt.Errorf("unknown firewall preset %q", preset)
	}
	if err := ValidatePort(servicePort); err != nil {
		return nil, fmt.Errorf("service port: %w", err)
	}

	specs := []PortSpec{{Start: servicePort, End: servicePort}, TierRange()}
	specs = append(specs, presetExtras(preset)...)

	if preset == PresetCustom {
		if len(custom) == 0 {
			return nil, fmt.Errorf("custom preset requires at least one port")
		}
		for _, s := range custom {
			if err := ValidateSpec(s); err != nil {
				return nil, err
			}
		}
		specs = append(specs, custom...)
	}

	return merge(specs), nil
}

func ValidatePort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %d out of range 1-65535", p)
	}
	return nil
}

func ValidateSpec(s PortSpec) error {
	if err := ValidatePort(s.Start); err != nil {
		return err
	}
	if err := ValidatePort(s.End); err != nil {
		return err
	}
	if s.End < s.Start {
		return fmt.Errorf("port range %d-%d ends before it starts", s.Start, s.End)
	}
	return nil
}

// merge sorts specs and coalesces any that overlap or touch.
func merge(in []PortSpec) []PortSpec {
	if len(in) == 0 {
		return nil
	}
	sorted := make([]PortSpec, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})

	out := []PortSpec{sorted[0]}
	for _, s := range sorted[1:] {
		last := &out[len(out)-1]
		if s.Start <= last.End+1 {
			if s.End > last.End {
				last.End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// FormatPorts renders specs as the comma-separated form the install script
// parses, e.g. "443,20001-22000".
func FormatPorts(specs []PortSpec) string {
	parts := make([]string, 0, len(specs))
	for _, s := range specs {
		parts = append(parts, s.String())
	}
	return strings.Join(parts, ",")
}

// ParsePorts reads the form FormatPorts produces.
func ParsePorts(s string) ([]PortSpec, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, nil
	}

	var specs []PortSpec
	for _, field := range strings.Split(trimmed, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		start, end, found := strings.Cut(field, "-")
		lo, err := strconv.Atoi(strings.TrimSpace(start))
		if err != nil {
			return nil, fmt.Errorf("invalid port %q", field)
		}
		hi := lo
		if found {
			hi, err = strconv.Atoi(strings.TrimSpace(end))
			if err != nil {
				return nil, fmt.Errorf("invalid port range %q", field)
			}
		}
		spec := PortSpec{Start: lo, End: hi}
		if err := ValidateSpec(spec); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return merge(specs), nil
}
