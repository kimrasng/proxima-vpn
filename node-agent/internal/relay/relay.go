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
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/netip"
	"os/exec"
	"sort"
	"strconv"
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

// resetCounters reads and zeroes every named counter in the table in one
// kernel transaction, so no forwarded byte is counted twice or skipped between
// the read and the reset. Overridable in tests.
var resetCounters = func() ([]byte, error) {
	cmd := exec.Command("nft", "-j", "reset", "counters", "table", "inet", tableName)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if problem := strings.TrimSpace(stderr.String()); problem != "" {
			return nil, fmt.Errorf("nft reset counters: %w: %s", err, problem)
		}
		return nil, fmt.Errorf("nft reset counters: %w", err)
	}
	return stdout.Bytes(), nil
}

// Manager owns the relay nftables table and the forwarding sysctl.
type Manager struct {
	mu      sync.Mutex
	applied string
	// status is the outcome of the last policy reconciliation, reported in
	// the heartbeat so the panel can show whether each route is in force.
	status PolicyStatus
	// counted is whether the installed table carries per-route counters.
	counted bool
	// carried holds bytes read out of counters that have not been handed to
	// DrainTraffic yet. Replacing the table zeroes its counters, so they are
	// read into here first instead of being lost with the old table.
	carried map[routeCounterKey]routeCounts
}

type routeCounterKey struct {
	port      int
	transport nodeprov.Transport
}

type routeCounts struct{ up, down int64 }

// PolicyStatus is what the agent tells the panel about its forwarding policy.
// Fingerprint names the policy actually installed (nodeprov.PolicyFingerprint)
// and only changes when an apply succeeds; Error describes the most recent
// failed attempt and clears on the next success, so a failure never erases the
// record of what is still running.
type PolicyStatus struct {
	Fingerprint string
	Error       string
}

// Status returns the last reconciliation outcome.
func (m *Manager) Status() PolicyStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// RecordFailure notes a reconciliation that failed before Apply ran (for
// example the rules could not be fetched), keeping the installed fingerprint.
func (m *Manager) RecordFailure(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Error = err.Error()
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
	tcpPorts, udpPorts := counterPorts(sorted)

	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s {\n", tableName)
	if role.Forwards() {
		if len(tcpPorts) > 0 {
			// Record the post-NAT tuple and original client together. Including
			// the original client in the key exposes tuple reuse as ambiguity
			// rather than silently attributing a connection to the wrong client.
			b.WriteString("\tset client_flows {\n\t\ttype ipv4_addr . inet_service . ipv4_addr . inet_service . ipv4_addr\n\t\tflags timeout\n\t\ttimeout 5m\n\t\tsize 262144\n\t}\n")
		}
		// One named counter per route, protocol and direction. Named counters
		// can be read and zeroed atomically (nft reset counters), which is what
		// lets the agent report deltas without a race against new packets.
		for _, set := range []struct {
			transport nodeprov.Transport
			ports     []int
		}{{nodeprov.TransportTCP, tcpPorts}, {nodeprov.TransportUDP, udpPorts}} {
			for _, port := range set.ports {
				fmt.Fprintf(&b, "\tcounter %s { }\n", counterName("up", set.transport, port))
				fmt.Fprintf(&b, "\tcounter %s { }\n", counterName("dn", set.transport, port))
			}
		}
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
		if len(tcpPorts) > 0 {
			// srcnat runs first, so the tuple matches what the Exit sees.
			// This rule has no verdict and never changes packet forwarding.
			b.WriteString("\tchain observe_flows {\n\t\ttype filter hook postrouting priority srcnat + 10; policy accept;\n\t\tct status dnat ct direction original meta l4proto tcp update @client_flows { ip saddr . tcp sport . ip daddr . tcp dport . ct original ip saddr }\n\t}\n")
		}

		// A relayed packet is redirected in prerouting and leaves through forward, so
		// a DROP policy there silently blackholes every chain. Accepting only the
		// conntrack states belonging to a rewritten flow keeps that from turning the
		// relay into an open router.
		fmt.Fprintf(&b, "\tchain forward {\n")
		fmt.Fprintf(&b, "\t\ttype filter hook forward priority filter; policy accept;\n")
		// Count before any verdict. The conntrack original destination port
		// is the entry port the client dialled for both directions of a
		// rewritten flow, so one lookup attributes uploads and replies to the
		// same route. These rules have no verdict and a port missing from the
		// map simply does not match, so counting can never change forwarding.
		for _, set := range []struct {
			transport nodeprov.Transport
			ports     []int
		}{{nodeprov.TransportTCP, tcpPorts}, {nodeprov.TransportUDP, udpPorts}} {
			if len(set.ports) == 0 {
				continue
			}
			for _, direction := range []struct{ name, ct string }{{"up", "original"}, {"dn", "reply"}} {
				entries := make([]string, len(set.ports))
				for i, port := range set.ports {
					entries[i] = fmt.Sprintf("%d : %q", port, counterName(direction.name, set.transport, port))
				}
				fmt.Fprintf(&b, "\t\tct status dnat meta l4proto %s ct direction %s counter name ct original proto-dst map { %s }\n",
					set.transport, direction.ct, strings.Join(entries, ", "))
			}
		}
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

// counterPorts lists the entry ports that get a counter for each protocol; a
// tcp_udp rule is counted separately per protocol.
func counterPorts(rules []nodeprov.RelayRule) (tcp, udp []int) {
	for _, r := range rules {
		if r.Transport.Covers(nodeprov.TransportTCP) {
			tcp = append(tcp, r.EntryPort)
		}
		if r.Transport.Covers(nodeprov.TransportUDP) {
			udp = append(udp, r.EntryPort)
		}
	}
	return tcp, udp
}

// counterName is the nftables object name of one route counter. direction is
// "up" (client to Exit) or "dn" (replies).
func counterName(direction string, transport nodeprov.Transport, port int) string {
	return fmt.Sprintf("route_%s_%s_%d", direction, transport, port)
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
		m.RecordFailure(err)
		return err
	}
	fingerprint := nodeprov.PolicyFingerprint(role, rules, exitRules)

	m.mu.Lock()
	defer m.mu.Unlock()

	if script == m.applied {
		m.status = PolicyStatus{Fingerprint: fingerprint}
		return nil
	}

	// Replacing the table zeroes every counter in it, including those of
	// routes that stay. Read them out first. A table left by a previous agent
	// process is read too, so a restart does not drop its last interval.
	if m.counted || (m.applied == "" && role.Forwards()) {
		_ = m.harvestLocked(m.counted) // logged inside; never blocks policy
	}

	// A direct-mode Exit with no relay-only listeners has nothing to filter:
	// the rendered input chain is empty and accepts everything. Requiring
	// nftables there only breaks unprivileged hosts (e.g. NAT containers
	// without CAP_NET_ADMIN) while adding no protection; Xray's API and the
	// device-egress SOCKS bind loopback regardless.
	//
	// A table left by an earlier relay-protected policy (possibly from a
	// previous agent process) must still be removed. The add-then-delete
	// script cannot fail on a missing table, only on missing permission - and
	// without permission no such table could have been installed - so its
	// error is not fatal here.
	if !role.Forwards() && !hasRelayOnlyExit(exitRules) {
		if err := runScript(fmt.Sprintf("table inet %s\ndelete table inet %s\n", tableName, tableName)); err != nil {
			log.Printf("node policy: direct Exit needs no nftables table; skipping (%v)", err)
		}
		m.applied = script
		m.counted = false
		m.status = PolicyStatus{Fingerprint: fingerprint}
		return nil
	}

	if role.Forwards() && len(rules) > 0 {
		if err := enableForwarding(); err != nil {
			m.status.Error = err.Error()
			return err
		}
	}
	if err := runScript(script); err != nil {
		m.status.Error = err.Error()
		return err
	}

	m.applied = script
	m.counted = role.Forwards() && len(rules) > 0
	m.status = PolicyStatus{Fingerprint: fingerprint}
	return nil
}

// hasRelayOnlyExit reports whether any Exit port must be restricted to relay
// addresses. Those rules exist to make a non-published Exit unreachable
// directly; they need nftables, and failing to install them must fail closed.
func hasRelayOnlyExit(exitRules []nodeprov.ExitRule) bool {
	return len(exitRules) > 0
}

// Clear removes the table, so a node that stops being a relay stops forwarding.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.counted {
		_ = m.harvestLocked(true) // logged inside; never blocks Clear
	}
	script := fmt.Sprintf("table inet %s\ndelete table inet %s\n", tableName, tableName)
	if err := runScript(script); err != nil {
		return err
	}
	m.applied = ""
	m.counted = false
	return nil
}

// DrainTraffic returns the bytes each route forwarded since the previous
// drain and forgets them. The caller owns the result: if it cannot deliver
// it, it must hand it back with ReturnTraffic or the bytes are lost.
//
// Delivery is at least once. A report the control plane stored but whose
// response never arrived is returned and sent again, counting that interval
// twice; for a diagnostic per-route figure that beats silently dropping it.
func (m *Manager) DrainTraffic() ([]nodeprov.RouteTraffic, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	if m.counted {
		err = m.harvestLocked(false)
	}
	out := make([]nodeprov.RouteTraffic, 0, len(m.carried))
	for key, counts := range m.carried {
		if counts.up == 0 && counts.down == 0 {
			continue
		}
		out = append(out, nodeprov.RouteTraffic{EntryPort: key.port, Transport: key.transport, UpBytes: counts.up, DownBytes: counts.down})
	}
	m.carried = nil
	sort.Slice(out, func(i, j int) bool {
		if out[i].EntryPort != out[j].EntryPort {
			return out[i].EntryPort < out[j].EntryPort
		}
		return out[i].Transport < out[j].Transport
	})
	return out, err
}

// ReturnTraffic puts back a drained report that could not be delivered, so
// the next drain sends it again.
func (m *Manager) ReturnTraffic(traffic []nodeprov.RouteTraffic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range traffic {
		m.carryLocked(routeCounterKey{t.EntryPort, t.Transport}, routeCounts{t.UpBytes, t.DownBytes})
	}
}

// harvestLocked reads and zeroes the installed counters into carried. A
// failure loses only that interval's figures and never blocks forwarding
// policy, so it is logged when the caller expected counters to be there.
func (m *Manager) harvestLocked(logFailure bool) error {
	data, err := resetCounters()
	if err == nil {
		var counts map[routeCounterKey]routeCounts
		if counts, err = parseRouteCounters(data); err == nil {
			for key, c := range counts {
				m.carryLocked(key, c)
			}
			return nil
		}
	}
	if logFailure {
		log.Printf("route traffic: %v", err)
	}
	return err
}

func (m *Manager) carryLocked(key routeCounterKey, c routeCounts) {
	if m.carried == nil {
		m.carried = make(map[routeCounterKey]routeCounts)
	}
	cur := m.carried[key]
	m.carried[key] = routeCounts{saturatingAdd(cur.up, c.up), saturatingAdd(cur.down, c.down)}
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// parseRouteCounters extracts route counters from `nft -j` output. Objects
// this package did not name are ignored.
func parseRouteCounters(data []byte) (map[routeCounterKey]routeCounts, error) {
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Name  string `json:"name"`
				Table string `json:"table"`
				Bytes int64  `json:"bytes"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse nft counters: %w", err)
	}
	out := make(map[routeCounterKey]routeCounts)
	for _, item := range doc.Nftables {
		if item.Counter == nil || item.Counter.Table != tableName || item.Counter.Bytes <= 0 {
			continue
		}
		parts := strings.Split(item.Counter.Name, "_")
		if len(parts) != 4 || parts[0] != "route" {
			continue
		}
		port, err := strconv.Atoi(parts[3])
		transport := nodeprov.Transport(parts[2])
		if err != nil || nodeprov.ValidatePort(port) != nil || (transport != nodeprov.TransportTCP && transport != nodeprov.TransportUDP) {
			continue
		}
		key := routeCounterKey{port, transport}
		cur := out[key]
		switch parts[1] {
		case "up":
			cur.up = saturatingAdd(cur.up, item.Counter.Bytes)
		case "dn":
			cur.down = saturatingAdd(cur.down, item.Counter.Bytes)
		default:
			continue
		}
		out[key] = cur
	}
	return out, nil
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
