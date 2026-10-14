package services

import (
	"fmt"
	"sort"
)

// staleAddressIdleMillis is how long a client address's every transport must
// have gone without acknowledging anything before the address is treated as
// abandoned rather than merely quiet.
//
// This exists for network switches. A phone moving from Wi-Fi to LTE connects
// from its new address while the old connection lingers, silent, until TCP
// gives up on it, so one device briefly shows as two addresses. Counting them
// equally would deny the newest address, which is the one the user is actually
// using, and keep the dead one. Two minutes is well above the keepalive period
// of a live idle tunnel (Go's listener default is 15 s) and far below the
// minutes a dead connection survives, so a quiet but connected client is not
// mistaken for a dead one.
//
// Idle is measured on the Exit's socket. Behind a NAT-forwarding Entry the
// acknowledgements still originate at the client. If an Entry were a proxy, its
// own acknowledgements would keep Idle near zero and no address would ever
// look stale, which degrades safely to the plain newest-denied order.
const staleAddressIdleMillis = 120_000

// addressIsStale reports whether the observation proves an address abandoned.
// Absent idle evidence is never stale.
func addressIsStale(observation ClientAddressObservation, address string) bool {
	idle, known := observation.Idle[address]
	return known && idle >= staleAddressIdleMillis
}

// ClientAddressPolicy is a conservative proposal for address-qualified egress.
// Known is false if observation/order is incomplete. It is not an applied
// policy acknowledgement and must never be advertised as enforcement success.
type ClientAddressPolicy struct {
	Known   bool
	Allowed []string
	Denied  []string
	Reason  string
}

// ProposeClientAddressPolicy keeps addresses in a fixed priority order and
// rejects whatever falls beyond the cap: addresses that are still active come
// before abandoned ones, and within each group older cohorts come before newer
// ones. An abandoned address is therefore denied first, so a network switch
// does not cost the client its live connection. Equal starts at the cutoff are
// ambiguous: choosing lexicographically would arbitrarily disconnect a
// legitimate client. A cutoff that separates an active address from an
// abandoned one is never ambiguous, however their starts compare.
func ProposeClientAddressPolicy(observation ClientAddressObservation, limit int) (ClientAddressPolicy, error) {
	out := ClientAddressPolicy{Allowed: []string{}, Denied: []string{}}
	if !observation.Complete {
		out.Reason = "incomplete_observation"
		return out, nil
	}
	seen := map[string]bool{}
	addresses := append([]string(nil), observation.Addresses...)
	for _, address := range addresses {
		if seen[address] {
			return out, fmt.Errorf("duplicate observed client address")
		}
		seen[address] = true
	}
	if limit <= 0 || len(addresses) <= limit {
		sort.Strings(addresses)
		out.Known = true
		out.Allowed = addresses
		return out, nil
	}
	for _, address := range addresses {
		if observation.Starts[address] <= 0 {
			out.Reason = "unknown_start"
			return out, nil
		}
	}
	sort.Slice(addresses, func(i, j int) bool {
		a, b := addresses[i], addresses[j]
		if staleA, staleB := addressIsStale(observation, a), addressIsStale(observation, b); staleA != staleB {
			return !staleA
		}
		if observation.Starts[a] != observation.Starts[b] {
			return observation.Starts[a] < observation.Starts[b]
		}
		return a < b
	})
	kept, first := addresses[limit-1], addresses[limit]
	if addressIsStale(observation, kept) == addressIsStale(observation, first) &&
		observation.Starts[kept] == observation.Starts[first] {
		out.Reason = "ambiguous_cutoff"
		return out, nil
	}
	out.Known = true
	out.Allowed = append(out.Allowed, addresses[:limit]...)
	out.Denied = append(out.Denied, addresses[limit:]...)
	sort.Strings(out.Allowed)
	sort.Strings(out.Denied)
	return out, nil
}
