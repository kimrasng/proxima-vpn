// Package relay installs the packet-forwarding rules a relay node applies. A
// relay rewrites the destination of traffic arriving on a chain's entry port to
// the chain's exit node and forwards it unchanged; it never terminates the
// tunnel, so one rule set carries VLESS Reality, Hysteria2 or WireGuard without
// knowing which it is.
//
// Forwarding is done in the kernel with nftables rather than in a userspace
// proxy, because a userspace hop would copy every packet and QUIC (Hysteria2)
// and WireGuard are both UDP, where that cost and the added jitter show up
// directly as throughput loss.
package relay

import (
	"bytes"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// tableName is the nftables table this package owns outright. Every apply
// replaces its whole contents, so nothing else may live in it.
const tableName = "proxima_relay"

// runCmd runs a command; overridable in tests.
//
// nft reports some failures on stderr while exiting 0, so an exit-status-only
// check reads a refused rule as installed - the same trap tc sets in
// internal/shaper, and the reason a relay could otherwise report healthy while
// forwarding nothing.
var runCmd = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	problem := strings.TrimSpace(stderr.String())
	if runErr != nil {
		if problem != "" {
			return fmt.Errorf("%s: %w: %s", name, runErr, problem)
		}
		return fmt.Errorf("%s: %w", name, runErr)
	}
	if problem != "" {
		return fmt.Errorf("%s reported: %s", name, problem)
	}
	return nil
}

// runScript feeds a ruleset to nft on stdin, which is how a whole table is
// replaced in one atomic transaction.
var runScript = func(script string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	problem := strings.TrimSpace(stderr.String())
	if runErr != nil {
		if problem != "" {
			return fmt.Errorf("nft -f: %w: %s", runErr, problem)
		}
		return fmt.Errorf("nft -f: %w", runErr)
	}
	if problem != "" {
		return fmt.Errorf("nft reported: %s", problem)
	}
	return nil
}

// Manager owns the relay nftables table and the forwarding sysctl.
type Manager struct {
	mu      sync.Mutex
	applied string
}

func NewManager() *Manager { return &Manager{} }

// Render builds the complete nftables ruleset for the given rules.
//
// The table is flushed and rebuilt in the same script so nft applies it as one
// transaction: a half-written rule set would forward some chains and blackhole
// others. Rules are emitted as verdict maps rather than one rule per port so the
// lookup stays a single hash regardless of how many chains a pool carries.
//
// Rules are sorted first, so an unchanged set always renders identical text and
// the caller can skip a reload it does not need.
func Render(role nodeprov.Role, rules []nodeprov.RelayRule, exitRules []nodeprov.ExitRule) (string, error) {
	if !role.Valid() {
		return "", fmt.Errorf("unknown node role %q", role)
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			return "", err
		}
	}
	for _, r := range exitRules {
		if err := r.Validate(); err != nil {
			return "", err
		}
	}

	sorted := make([]nodeprov.RelayRule, len(rules))
	copy(sorted, rules)
	nodeprov.SortRules(sorted)

	tcp, udp := dnatMapEntries(sorted)

	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", tableName)
	if role.Forwards() {
		fmt.Fprintf(&b, "\tchain prerouting {\n")
		fmt.Fprintf(&b, "\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
		if tcp != "" {
			fmt.Fprintf(&b, "\t\tdnat ip to tcp dport map { %s }\n", tcp)
		}
		if udp != "" {
			fmt.Fprintf(&b, "\t\tdnat ip to udp dport map { %s }\n", udp)
		}
		fmt.Fprintf(&b, "\t}\n")

		// Without masquerade the exit would answer straight to the client, which has
		// no conntrack entry for that address and drops it. The replies must retrace
		// the request's path, so the relay has to own the source address too.
		fmt.Fprintf(&b, "\tchain postrouting {\n")
		fmt.Fprintf(&b, "\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
		for _, ip := range exitIPs(sorted) {
			fmt.Fprintf(&b, "\t\tip daddr %s masquerade\n", ip)
		}
		fmt.Fprintf(&b, "\t}\n")

		// A relayed packet is redirected in prerouting and leaves through forward, so
		// a DROP policy there silently blackholes every chain. Accepting only the
		// conntrack states belonging to a rewritten flow keeps that from turning the
		// relay into an open router.
		fmt.Fprintf(&b, "\tchain forward {\n")
		fmt.Fprintf(&b, "\t\ttype filter hook forward priority filter; policy accept;\n")
		fmt.Fprintf(&b, "\t\tct state established,related accept\n")
		for _, ip := range exitIPs(sorted) {
			fmt.Fprintf(&b, "\t\tip daddr %s accept\n", ip)
		}
		fmt.Fprintf(&b, "\t}\n")
	}
	if role.Exits() {
		fmt.Fprintf(&b, "\tchain input {\n")
		fmt.Fprintf(&b, "\t\ttype filter hook input priority filter; policy accept;\n")
		// Merge chains that protect the same transport/port; do not let a
		// second empty rule drop an otherwise allowed pool member.
		type portKey struct {
			transport nodeprov.Transport
			port      int
		}
		allowed := make(map[portKey]map[string]struct{})
		for _, rule := range exitRules {
			for _, transport := range []nodeprov.Transport{nodeprov.TransportTCP, nodeprov.TransportUDP} {
				if !rule.Transport.Covers(transport) {
					continue
				}
				key := portKey{transport, rule.ExitPort}
				if allowed[key] == nil {
					allowed[key] = make(map[string]struct{})
				}
				for _, ip := range rule.RelayIPs {
					allowed[key][ip] = struct{}{}
				}
			}
		}
		keys := make([]portKey, 0, len(allowed))
		for key := range allowed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].port != keys[j].port {
				return keys[i].port < keys[j].port
			}
			return keys[i].transport < keys[j].transport
		})
		for _, key := range keys {
			ips := make([]netip.Addr, 0, len(allowed[key]))
			for ip := range allowed[key] {
				addr, _ := netip.ParseAddr(ip)
				ips = append(ips, addr)
			}
			sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
			if len(ips) > 0 {
				parts := make([]string, len(ips))
				for i, ip := range ips {
					parts[i] = ip.String()
				}
				fmt.Fprintf(&b, "\t\tip saddr { %s } %s dport %d accept\n", strings.Join(parts, ", "), key.transport, key.port)
			}
			fmt.Fprintf(&b, "\t\t%s dport %d drop\n", key.transport, key.port)
		}
		fmt.Fprintf(&b, "\t}\n")
	}
	fmt.Fprintf(&b, "}\n")

	return fmt.Sprintf("table inet %s\ndelete table inet %s\n%s", tableName, tableName, b.String()), nil
}

// dnatMapEntries renders the verdict-map bodies for each protocol. A tcp_udp
// rule appears in both, which is what makes it claim the port for each.
func dnatMapEntries(rules []nodeprov.RelayRule) (tcp, udp string) {
	var tcpParts, udpParts []string
	for _, r := range rules {
		entry := fmt.Sprintf("%d : %s . %d", r.EntryPort, r.ExitIP, r.ExitPort)
		switch r.Transport {
		case nodeprov.TransportTCP:
			tcpParts = append(tcpParts, entry)
		case nodeprov.TransportUDP:
			udpParts = append(udpParts, entry)
		case nodeprov.TransportTCPUDP:
			tcpParts = append(tcpParts, entry)
			udpParts = append(udpParts, entry)
		}
	}
	return strings.Join(tcpParts, ", "), strings.Join(udpParts, ", ")
}

// exitIPs lists each distinct exit address once, in first-seen order so the
// rendered text stays stable for an unchanged rule set.
func exitIPs(rules []nodeprov.RelayRule) []string {
	seen := make(map[string]bool, len(rules))
	var out []string
	for _, r := range rules {
		if seen[r.ExitIP] {
			continue
		}
		seen[r.ExitIP] = true
		out = append(out, r.ExitIP)
	}
	return out
}

// Apply installs the rules, replacing whatever the table held before. It is a
// no-op when the rendered ruleset matches what was last applied, so the common
// case of an unchanged poll costs nothing.
//
// The whole table is replaced rather than diffed: a diff has to be right about
// what is already installed, and when it is wrong the table keeps a stale rule
// that quietly sends a chain to the wrong exit.
func (m *Manager) Apply(role nodeprov.Role, rules []nodeprov.RelayRule, exitRules []nodeprov.ExitRule) error {
	script, err := Render(role, rules, exitRules)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if script == m.applied {
		return nil
	}

	if role.Forwards() && len(rules) > 0 {
		if err := enableForwarding(); err != nil {
			return err
		}
	}
	if err := runScript(script); err != nil {
		return err
	}

	m.applied = script
	return nil
}

// Clear removes the table, so a node that stops being a relay stops forwarding.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	script := fmt.Sprintf("table inet %s\ndelete table inet %s\n", tableName, tableName)
	if err := runScript(script); err != nil {
		return err
	}
	m.applied = ""
	return nil
}

// enableForwarding turns on IP forwarding, without which the kernel drops the
// rewritten packet instead of routing it on to the exit.
func enableForwarding() error {
	if err := runCmd("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable ipv4 forwarding: %w", err)
	}
	if err := runCmd("sysctl", "-w", "net.ipv6.conf.all.forwarding=1"); err != nil {
		return fmt.Errorf("enable ipv6 forwarding: %w", err)
	}
	return nil
}
