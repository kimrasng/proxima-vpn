package nodeprov

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// PolicyFingerprint identifies the forwarding policy a node should run: its
// role plus the relay and exit rules it is meant to install. The control plane
// computes it from its own state and the agent from the rules it applied, so
// equal fingerprints prove the node runs exactly the current policy - without
// a generation counter that every chain, pool or node edit would have to bump.
//
// Rules are canonicalised first (sorted, relay IPs sorted, nil equal to empty)
// so ordering differences between the API response and the database never
// read as a change.
func PolicyFingerprint(role Role, relay []RelayRule, exit []ExitRule) string {
	relaySorted := make([]RelayRule, len(relay))
	copy(relaySorted, relay)
	SortRules(relaySorted)

	exitSorted := make([]ExitRule, len(exit))
	for i, rule := range exit {
		ips := make([]string, len(rule.RelayIPs))
		copy(ips, rule.RelayIPs)
		sort.Strings(ips)
		rule.RelayIPs = ips
		exitSorted[i] = rule
	}
	SortExitRules(exitSorted)

	canonical, err := json.Marshal(struct {
		Version int         `json:"v"`
		Role    Role        `json:"role"`
		Relay   []RelayRule `json:"relay"`
		Exit    []ExitRule  `json:"exit"`
	}{1, role, relaySorted, exitSorted})
	if err != nil {
		// Only plain strings and ints are marshalled; this cannot fail.
		panic(err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
