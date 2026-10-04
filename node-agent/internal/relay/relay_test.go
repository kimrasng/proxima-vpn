package relay

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestRenderNftablesSyntax(t *testing.T) {
	if os.Getenv("NFT_CHECK") != "1" {
		t.Skip("requires nftables")
	}
	for _, role := range []nodeprov.Role{nodeprov.RoleExit, nodeprov.RoleRelay, nodeprov.RoleBoth} {
		script, err := Render(role, []nodeprov.RelayRule{{EntryPort: 5223, Transport: nodeprov.TransportUDP, ExitIP: "172.30.0.10", ExitPort: 51820}}, []nodeprov.ExitRule{{ExitPort: 51820, Transport: nodeprov.TransportUDP, RelayIPs: []string{"172.30.0.20"}}})
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command("nft", "-c", "-f", "-")
		command.Stdin = strings.NewReader(script)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s nft check: %v: %s", role, err, output)
		}
	}
}

func TestRenderEmitsOneVerdictMapPerProtocol(t *testing.T) {
	script, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
		{EntryPort: 5223, Transport: nodeprov.TransportUDP, ExitIP: "203.0.113.12", ExitPort: 8443},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(script, "dnat ip to tcp dport map { 5993 : 203.0.113.10 . 443 }") {
		t.Errorf("tcp map missing or malformed:\n%s", script)
	}
	if !strings.Contains(script, "dnat ip to udp dport map { 5223 : 203.0.113.12 . 8443 }") {
		t.Errorf("udp map missing or malformed:\n%s", script)
	}
}

// A tcp_udp chain forwards both protocols, so it has to appear in both maps -
// emitting it once would leave half the traffic unforwarded.
func TestRenderPlacesTCPUDPRuleInBothMaps(t *testing.T) {
	script, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 7104, Transport: nodeprov.TransportTCPUDP, ExitIP: "203.0.113.20", ExitPort: 51820},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := "7104 : 203.0.113.20 . 51820"
	if !strings.Contains(script, "tcp dport map { "+entry+" }") {
		t.Errorf("tcp_udp rule missing from the tcp map:\n%s", script)
	}
	if !strings.Contains(script, "udp dport map { "+entry+" }") {
		t.Errorf("tcp_udp rule missing from the udp map:\n%s", script)
	}
}

// Replies have to retrace the request's path: without masquerade the exit answers
// the client directly, which has no conntrack entry for that source and drops it.
func TestRenderMasqueradesEveryExit(t *testing.T) {
	script, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
		{EntryPort: 5994, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.11", ExitPort: 443},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, ip := range []string{"203.0.113.10", "203.0.113.11"} {
		if !strings.Contains(script, "ip daddr "+ip+" masquerade") {
			t.Errorf("exit %s is not masqueraded:\n%s", ip, script)
		}
	}
}

// Two chains to the same exit must not produce two identical masquerade rules.
func TestRenderDeduplicatesExits(t *testing.T) {
	script, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
		{EntryPort: 5223, Transport: nodeprov.TransportUDP, ExitIP: "203.0.113.10", ExitPort: 8443},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := strings.Count(script, "ip daddr 203.0.113.10 masquerade"); got != 1 {
		t.Errorf("masquerade rule for one exit emitted %d times, want 1:\n%s", got, script)
	}
}

// A relayed packet leaves through the forward hook, so a ruleset that omits it
// blackholes every chain on hosts whose forward policy is DROP.
func TestRenderAcceptsForwardedTraffic(t *testing.T) {
	script, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(script, "hook forward") {
		t.Errorf("no forward chain:\n%s", script)
	}
	if !strings.Contains(script, "ct state established,related accept") {
		t.Errorf("return traffic is not accepted:\n%s", script)
	}
	if !strings.Contains(script, "ip daddr 203.0.113.10 accept") {
		t.Errorf("traffic to the exit is not accepted:\n%s", script)
	}
}

// nft applies one script as a single transaction, so the table has to be dropped
// and rebuilt in the same input: a partially applied set would forward some
// chains and blackhole others.
func TestRenderReplacesTheTableAtomically(t *testing.T) {
	script, err := Render(nodeprov.RoleRelay, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	del := strings.Index(script, "delete table inet "+tableName)
	create := strings.Index(script, "table inet "+tableName+" {")
	if del < 0 || create < 0 {
		t.Fatalf("script does not both delete and recreate the table:\n%s", script)
	}
	if del > create {
		t.Errorf("table is recreated before it is deleted:\n%s", script)
	}
}

// An unchanged rule set must render identical text, or Apply cannot tell a real
// change from a reordered response and reloads nftables on every poll.
func TestRenderIsStableUnderReordering(t *testing.T) {
	a, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
		{EntryPort: 5223, Transport: nodeprov.TransportUDP, ExitIP: "203.0.113.12", ExitPort: 8443},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 5223, Transport: nodeprov.TransportUDP, ExitIP: "203.0.113.12", ExitPort: 8443},
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != b {
		t.Errorf("reordering the same rules changed the ruleset:\n%s\n---\n%s", a, b)
	}
}

func TestRenderRejectsInvalidRules(t *testing.T) {
	for name, rule := range map[string]nodeprov.RelayRule{
		"no exit ip":        {EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitPort: 443},
		"bad entry port":    {EntryPort: 0, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
		"bad exit port":     {EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 70000},
		"unknown transport": {EntryPort: 5993, Transport: "sctp", ExitIP: "203.0.113.10", ExitPort: 443},
	} {
		if _, err := Render(nodeprov.RoleRelay, []nodeprov.RelayRule{rule}, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestRenderExitAllowsOnlyPoolIPv4SourcesOnExactProtocolAndPort(t *testing.T) {
	script, err := Render(nodeprov.RoleExit, nil, []nodeprov.ExitRule{
		{ExitPort: 443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.10", "203.0.113.2"}},
	})
	if err != nil {
		t.Fatalf("render exit policy: %v", err)
	}

	allow := "ip saddr { 203.0.113.2, 203.0.113.10 } tcp dport 443 accept"
	drop := "tcp dport 443 drop"
	if !strings.Contains(script, allow) {
		t.Errorf("IPv4 pool allow rule missing:\n%s", script)
	}
	if !strings.Contains(script, drop) {
		t.Errorf("protocol/port drop rule missing:\n%s", script)
	}
	if strings.Index(script, allow) > strings.Index(script, drop) {
		t.Errorf("drop precedes pool allow rule:\n%s", script)
	}
	if strings.Contains(script, "ct state established") {
		t.Errorf("established traffic bypasses exit admission:\n%s", script)
	}
}

func TestRenderExitDropsProtectedPortWhenPoolIsEmpty(t *testing.T) {
	script, err := Render(nodeprov.RoleExit, nil, []nodeprov.ExitRule{
		{ExitPort: 8443, Transport: nodeprov.TransportTCPUDP},
	})
	if err != nil {
		t.Fatalf("render empty-pool exit policy: %v", err)
	}

	for _, rule := range []string{"tcp dport 8443 drop", "udp dport 8443 drop"} {
		if !strings.Contains(script, rule) {
			t.Errorf("empty pool is not fail-closed for %q:\n%s", rule, script)
		}
	}
}

func TestRenderUnionsPoolsSharingAProtectedPort(t *testing.T) {
	script, err := Render(nodeprov.RoleExit, nil, []nodeprov.ExitRule{
		{ExitPort: 51820, Transport: nodeprov.TransportUDP, RelayIPs: []string{"203.0.113.2"}},
		{ExitPort: 51820, Transport: nodeprov.TransportUDP},
		{ExitPort: 51820, Transport: nodeprov.TransportTCPUDP, RelayIPs: []string{"203.0.113.10"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "ip saddr { 203.0.113.2, 203.0.113.10 } udp dport 51820 accept") {
		t.Errorf("UDP pools were not combined: %s", script)
	}
	if strings.Count(script, "udp dport 51820 drop") != 1 {
		t.Errorf("expected one UDP protected-port drop: %s", script)
	}
	if !strings.Contains(script, "ip saddr { 203.0.113.10 } tcp dport 51820 accept") {
		t.Errorf("TCP policy inherited unrelated UDP sources: %s", script)
	}
}

func TestRenderExitDropAlsoCoversIPv6(t *testing.T) {
	script, err := Render(nodeprov.RoleExit, nil, []nodeprov.ExitRule{
		{ExitPort: 443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.2"}},
	})
	if err != nil {
		t.Fatalf("render exit policy: %v", err)
	}

	if strings.Contains(script, "ip6 saddr") {
		t.Errorf("exit allowlist must contain only IPv4 sources:\n%s", script)
	}
	if !strings.Contains(script, "tcp dport 443 drop") {
		t.Errorf("family-neutral drop does not protect the port from IPv6:\n%s", script)
	}
}

func TestRenderSeparatesAndCombinesRolePolicies(t *testing.T) {
	relayRules := []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
	}
	exitRules := []nodeprov.ExitRule{
		{ExitPort: 443, Transport: nodeprov.TransportTCP, RelayIPs: []string{"203.0.113.2"}},
	}

	tests := []struct {
		name      string
		role      nodeprov.Role
		wantDNAT  bool
		wantInput bool
	}{
		{name: "relay", role: nodeprov.RoleRelay, wantDNAT: true},
		{name: "exit", role: nodeprov.RoleExit, wantInput: true},
		{name: "both", role: nodeprov.RoleBoth, wantDNAT: true, wantInput: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script, err := Render(test.role, relayRules, exitRules)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got := strings.Contains(script, "hook prerouting"); got != test.wantDNAT {
				t.Errorf("prerouting present = %v, want %v", got, test.wantDNAT)
			}
			if got := strings.Contains(script, "hook input"); got != test.wantInput {
				t.Errorf("input present = %v, want %v", got, test.wantInput)
			}
		})
	}
}

func TestApplySkipsAnUnchangedRuleset(t *testing.T) {
	rules := []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
	}

	var scripts, commands int
	originalScript, originalCmd := runScript, runCmd
	t.Cleanup(func() { runScript, runCmd = originalScript, originalCmd })
	runScript = func(string) error { scripts++; return nil }
	runCmd = func(string, ...string) error { commands++; return nil }

	m := NewManager()
	if err := m.Apply(nodeprov.RoleRelay, rules, nil); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if scripts != 1 {
		t.Fatalf("first apply ran %d scripts, want 1", scripts)
	}

	if err := m.Apply(nodeprov.RoleRelay, rules, nil); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if scripts != 1 {
		t.Errorf("an unchanged ruleset was reapplied (%d scripts)", scripts)
	}

	if err := m.Apply(nodeprov.RoleRelay, append(rules, nodeprov.RelayRule{
		EntryPort: 5223, Transport: nodeprov.TransportUDP, ExitIP: "203.0.113.12", ExitPort: 8443,
	}), nil); err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if scripts != 2 {
		t.Errorf("a changed ruleset was not applied (%d scripts)", scripts)
	}
}

// Without ip_forward the kernel drops the rewritten packet instead of routing it
// to the exit, so a ruleset that installs cleanly still forwards nothing.
func TestApplyEnablesForwardingBeforeInstallingRules(t *testing.T) {
	var order []string
	originalScript, originalCmd := runScript, runCmd
	t.Cleanup(func() { runScript, runCmd = originalScript, originalCmd })
	runScript = func(string) error { order = append(order, "script"); return nil }
	runCmd = func(name string, args ...string) error {
		order = append(order, name+" "+strings.Join(args, " "))
		return nil
	}

	m := NewManager()
	if err := m.Apply(nodeprov.RoleRelay, []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
	}, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(order) == 0 || !strings.Contains(order[0], "net.ipv4.ip_forward=1") {
		t.Errorf("forwarding was not enabled first, got %v", order)
	}
	if order[len(order)-1] != "script" {
		t.Errorf("rules were not installed last, got %v", order)
	}
}

func TestApplyDoesNotEnableForwardingWithoutRelayRules(t *testing.T) {
	originalScript, originalCmd := runScript, runCmd
	t.Cleanup(func() { runScript, runCmd = originalScript, originalCmd })
	runScript = func(string) error { return nil }
	var commands int
	runCmd = func(string, ...string) error { commands++; return nil }

	err := NewManager().Apply(nodeprov.RoleExit, nil, []nodeprov.ExitRule{
		{ExitPort: 443, Transport: nodeprov.TransportTCP},
	})
	if err != nil {
		t.Fatalf("apply exit policy: %v", err)
	}
	if commands != 0 {
		t.Errorf("enabled forwarding %d time(s) for exit-only policy", commands)
	}
}

// A refused nft rule is reported on stderr with exit status 0, so an
// exit-status-only check would record a relay as forwarding when it is not.
func TestRunScriptTreatsStderrAsFailureEvenWhenTheCommandExitsZero(t *testing.T) {
	original := runCmd
	t.Cleanup(func() { runCmd = original })

	err := runCmd("sh", "-c", "echo 'Error: Could not process rule: Operation not permitted' >&2; exit 0")
	if err == nil {
		t.Fatal("a command that wrote to stderr and exited 0 was treated as success")
	}
	if !strings.Contains(err.Error(), "Operation not permitted") {
		t.Errorf("error should carry what nft reported, got: %v", err)
	}
}

func TestApplyDoesNotCacheAFailedRuleset(t *testing.T) {
	originalScript, originalCmd := runScript, runCmd
	t.Cleanup(func() { runScript, runCmd = originalScript, originalCmd })
	runCmd = func(string, ...string) error { return nil }

	var attempts int
	runScript = func(string) error {
		attempts++
		if attempts == 1 {
			return errRefused
		}
		return nil
	}

	rules := []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
	}

	m := NewManager()
	if err := m.Apply(nodeprov.RoleRelay, rules, nil); err == nil {
		t.Fatal("a failed apply was reported as success")
	}
	if err := m.Apply(nodeprov.RoleRelay, rules, nil); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if attempts != 2 {
		t.Errorf("the failed ruleset was cached; nft ran %d times, want 2", attempts)
	}
}

func TestApplyPreservesLastKnownGoodAfterFailedReplacement(t *testing.T) {
	originalScript, originalCmd := runScript, runCmd
	t.Cleanup(func() { runScript, runCmd = originalScript, originalCmd })
	runCmd = func(string, ...string) error { return nil }

	rulesA := []nodeprov.RelayRule{
		{EntryPort: 5993, Transport: nodeprov.TransportTCP, ExitIP: "203.0.113.10", ExitPort: 443},
	}
	rulesB := []nodeprov.RelayRule{
		{EntryPort: 5223, Transport: nodeprov.TransportUDP, ExitIP: "203.0.113.12", ExitPort: 8443},
	}
	scriptA, err := Render(nodeprov.RoleRelay, rulesA, nil)
	if err != nil {
		t.Fatalf("render policy A: %v", err)
	}
	scriptB, err := Render(nodeprov.RoleRelay, rulesB, nil)
	if err != nil {
		t.Fatalf("render policy B: %v", err)
	}

	var scripts []string
	runScript = func(script string) error {
		scripts = append(scripts, script)
		if len(scripts) == 2 {
			return errRefused
		}
		return nil
	}

	m := NewManager()
	if err := m.Apply(nodeprov.RoleRelay, rulesA, nil); err != nil {
		t.Fatalf("apply policy A: %v", err)
	}
	if err := m.Apply(nodeprov.RoleRelay, rulesB, nil); err == nil {
		t.Fatal("failed replacement was reported as success")
	}
	if err := m.Apply(nodeprov.RoleRelay, rulesA, nil); err != nil {
		t.Fatalf("reapply policy A after failed replacement: %v", err)
	}
	if len(scripts) != 2 {
		t.Fatalf("reapplying policy A ran %d scripts, want 2 total", len(scripts))
	}
	if err := m.Apply(nodeprov.RoleRelay, rulesB, nil); err != nil {
		t.Fatalf("retry policy B: %v", err)
	}

	want := []string{scriptA, scriptB, scriptB}
	if len(scripts) != len(want) {
		t.Fatalf("runScript calls = %d, want %d", len(scripts), len(want))
	}
	for i := range want {
		if scripts[i] != want[i] {
			t.Errorf("runScript call %d did not match policy sequence", i+1)
		}
	}
}

var errRefused = errRefusedType{}

type errRefusedType struct{}

func (errRefusedType) Error() string { return "nft refused the ruleset" }
