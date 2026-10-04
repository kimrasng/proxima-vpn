package nodeprov

import "testing"

func TestResolvePortsAlwaysIncludesTierRange(t *testing.T) {
	tier := TierRange()

	for _, preset := range Presets() {
		custom := []PortSpec{}
		if preset == PresetCustom {
			custom = []PortSpec{{Start: 8443, End: 8443}}
		}

		specs, err := ResolvePorts(RoleExit, preset, 443, custom)
		if err != nil {
			t.Fatalf("preset %q: unexpected error: %v", preset, err)
		}

		var covered bool
		for _, s := range specs {
			if s.Start <= tier.Start && s.End >= tier.End {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("preset %q resolved to %v, which does not cover tier range %v",
				preset, FormatPorts(specs), tier)
		}
	}
}

func TestResolvePortsStandardPreset(t *testing.T) {
	specs, err := ResolvePorts(RoleExit, PresetStandard, 443, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := FormatPorts(specs), "443,20001-22000"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePortsWebAltAddsPort80(t *testing.T) {
	specs, err := ResolvePorts(RoleExit, PresetWebAlt, 443, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := FormatPorts(specs), "80,443,20001-22000"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePortsMergesAdjacentAndOverlapping(t *testing.T) {
	// 20001-22000 is the tier range; a custom spec touching it must coalesce
	// rather than produce a second redundant firewall rule.
	specs, err := ResolvePorts(RoleExit, PresetCustom, 443, []PortSpec{
		{Start: 22001, End: 22100},
		{Start: 500, End: 600},
		{Start: 550, End: 700},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := FormatPorts(specs), "443,500-700,20001-22100"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePortsCustomRequiresPorts(t *testing.T) {
	if _, err := ResolvePorts(RoleExit, PresetCustom, 443, nil); err == nil {
		t.Error("expected an error when the custom preset supplies no ports")
	}
}

func TestResolvePortsRejectsBadInput(t *testing.T) {
	if _, err := ResolvePorts(RoleExit, "nonsense", 443, nil); err == nil {
		t.Error("expected an error for an unknown preset")
	}
	if _, err := ResolvePorts(RoleExit, PresetStandard, 0, nil); err == nil {
		t.Error("expected an error for an out-of-range service port")
	}
	if _, err := ResolvePorts(RoleExit, PresetCustom, 443, []PortSpec{{Start: 900, End: 800}}); err == nil {
		t.Error("expected an error for a reversed range")
	}
}

// A relayed packet is redirected in PREROUTING and leaves through FORWARD, so it
// never reaches INPUT: opening the relay range there would protect nothing and
// would advertise the whole range to a port scanner.
func TestResolvePortsNeverOpensRelayRange(t *testing.T) {
	relay := RelayRange()

	for _, role := range Roles() {
		specs, err := ResolvePorts(role, PresetStandard, 443, nil)
		if err != nil {
			t.Fatalf("role %q: unexpected error: %v", role, err)
		}
		for _, s := range specs {
			if s.End >= relay.Start && s.Start <= relay.End {
				t.Errorf("role %q resolved to %q, which overlaps the relay range %v",
					role, FormatPorts(specs), relay)
			}
		}
	}
}

func TestRelayRangeDoesNotOverlapTierRange(t *testing.T) {
	tier := TierRange()
	relay := RelayRange()
	if relay.Start <= tier.End {
		t.Fatalf("relay range %v starts at or below the tier range end %v; a chain entry port could collide with a speed-tier inbound",
			relay, tier)
	}
}

func TestResolvePortsRejectsUnknownRole(t *testing.T) {
	if _, err := ResolvePorts("nonsense", PresetStandard, 443, nil); err == nil {
		t.Error("expected an error for an unknown role")
	}
}

func TestParsePortsRoundTrip(t *testing.T) {
	specs, err := ResolvePorts(RoleBoth, PresetWebAlt, 8443, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	formatted := FormatPorts(specs)

	parsed, err := ParsePorts(formatted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := FormatPorts(parsed); got != formatted {
		t.Errorf("round trip changed %q into %q", formatted, got)
	}
}

func TestParsePortsRejectsGarbage(t *testing.T) {
	for _, in := range []string{"abc", "443-", "70000", "443-70000", "0"} {
		if _, err := ParsePorts(in); err == nil {
			t.Errorf("expected an error parsing %q", in)
		}
	}
}

func TestParsePortsEmpty(t *testing.T) {
	specs, err := ParsePorts("  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("expected no specs, got %v", specs)
	}
}
