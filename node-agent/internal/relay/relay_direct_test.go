package relay

import (
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// A direct-mode Exit (no relay-only listeners) must start on hosts that deny
// nftables, such as NAT containers without CAP_NET_ADMIN.
func TestApplyDirectExitToleratesMissingNftablesPermission(t *testing.T) {
	calls := 0
	orig := runScript
	runScript = func(string) error { calls++; return errors.New("Operation not permitted") }
	t.Cleanup(func() { runScript = orig })

	m := NewManager()
	if err := m.Apply(nodeprov.RoleExit, nil, nil); err != nil {
		t.Fatalf("direct Exit without nftables must start, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one best-effort stale-table cleanup, got %d", calls)
	}
	// Unchanged policy on the next poll must not retry or log again.
	if err := m.Apply(nodeprov.RoleExit, nil, nil); err != nil || calls != 1 {
		t.Fatalf("unchanged direct policy re-ran nft: calls=%d err=%v", calls, err)
	}
}

// Relay-only Exit ports restrict who may connect; failing to install that
// protection must still fail closed, never silently expose the port.
func TestApplyProtectedExitStillFailsClosedWithoutNftables(t *testing.T) {
	orig := runScript
	runScript = func(string) error { return errors.New("Operation not permitted") }
	t.Cleanup(func() { runScript = orig })

	m := NewManager()
	rules := []nodeprov.ExitRule{{ExitPort: 8443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.10"}}}
	if err := m.Apply(nodeprov.RoleExit, nil, rules); err == nil {
		t.Fatal("protected Exit must not start without its nftables rules")
	}
}

// A relay always needs nftables for DNAT; it must keep failing closed.
func TestApplyRelayStillRequiresNftables(t *testing.T) {
	origScript, origCmd := runScript, runCmd
	runScript = func(string) error { return errors.New("Operation not permitted") }
	runCmd = func(string, ...string) error { return nil }
	t.Cleanup(func() { runScript, runCmd = origScript, origCmd })

	m := NewManager()
	rules := []nodeprov.RelayRule{{EntryPort: 443, ExitIP: "203.0.113.20", ExitPort: 8443, Transport: nodeprov.TransportTCP}}
	if err := m.Apply(nodeprov.RoleRelay, rules, nil); err == nil {
		t.Fatal("relay must not start without nftables")
	}
}

// When nftables works, switching a protected Exit to direct removes the old
// table instead of leaving its drop rules behind.
func TestApplyDirectExitRemovesStaleProtectedTable(t *testing.T) {
	var scripts []string
	orig := runScript
	runScript = func(s string) error { scripts = append(scripts, s); return nil }
	t.Cleanup(func() { runScript = orig })

	m := NewManager()
	protected := []nodeprov.ExitRule{{ExitPort: 8443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.10"}}}
	if err := m.Apply(nodeprov.RoleExit, nil, protected); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(nodeprov.RoleExit, nil, nil); err != nil {
		t.Fatal(err)
	}
	last := scripts[len(scripts)-1]
	if last != "table inet proxima_relay\ndelete table inet proxima_relay\n" {
		t.Fatalf("expected table removal, got %q", last)
	}
}
