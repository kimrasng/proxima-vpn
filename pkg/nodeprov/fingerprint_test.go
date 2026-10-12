package nodeprov

import "testing"

func TestPolicyFingerprintIgnoresOrderingAndNilSlices(t *testing.T) {
	relay := []RelayRule{
		{EntryPort: 24444, Transport: TransportTCP, ExitIP: "10.0.0.40", ExitPort: 8443},
		{EntryPort: 24443, Transport: TransportTCP, ExitIP: "10.0.0.30", ExitPort: 8443},
	}
	exit := []ExitRule{
		{ExitPort: 8443, Transport: TransportTCP, RelayIPs: []string{"10.0.0.21", "10.0.0.20"}},
		{ExitPort: 443, Transport: TransportUDP, RelayIPs: []string{"10.0.0.20"}},
	}
	reordered := []RelayRule{relay[1], relay[0]}
	reorderedExit := []ExitRule{
		{ExitPort: 443, Transport: TransportUDP, RelayIPs: []string{"10.0.0.20"}},
		{ExitPort: 8443, Transport: TransportTCP, RelayIPs: []string{"10.0.0.20", "10.0.0.21"}},
	}
	if PolicyFingerprint(RoleBoth, relay, exit) != PolicyFingerprint(RoleBoth, reordered, reorderedExit) {
		t.Fatal("reordered rules changed the fingerprint")
	}
	if PolicyFingerprint(RoleRelay, nil, nil) != PolicyFingerprint(RoleRelay, []RelayRule{}, []ExitRule{}) {
		t.Fatal("nil and empty rule sets fingerprint differently")
	}
	// Canonicalising must not mutate the caller's slices.
	if relay[0].EntryPort != 24444 || exit[0].RelayIPs[0] != "10.0.0.21" {
		t.Fatal("PolicyFingerprint reordered its input")
	}
}

func TestPolicyFingerprintChangesWithPolicy(t *testing.T) {
	base := []RelayRule{{EntryPort: 24443, Transport: TransportTCP, ExitIP: "10.0.0.30", ExitPort: 8443}}
	fp := PolicyFingerprint(RoleRelay, base, nil)
	for name, other := range map[string]string{
		"role":      PolicyFingerprint(RoleBoth, base, nil),
		"exit port": PolicyFingerprint(RoleRelay, []RelayRule{{EntryPort: 24443, Transport: TransportTCP, ExitIP: "10.0.0.30", ExitPort: 20100}}, nil),
		"transport": PolicyFingerprint(RoleRelay, []RelayRule{{EntryPort: 24443, Transport: TransportTCPUDP, ExitIP: "10.0.0.30", ExitPort: 8443}}, nil),
		"rule gone": PolicyFingerprint(RoleRelay, nil, nil),
		"exit rule": PolicyFingerprint(RoleRelay, base, []ExitRule{{ExitPort: 8443, Transport: TransportTCP}}),
	} {
		if other == fp {
			t.Errorf("%s change did not change the fingerprint", name)
		}
	}
}
