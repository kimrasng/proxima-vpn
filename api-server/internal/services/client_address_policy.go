package services

import (
	"fmt"
	"sort"
)

// ClientAddressPolicy is a conservative proposal for address-qualified egress.
// Known is false if observation/order is incomplete. It is not an applied
// policy acknowledgement and must never be advertised as enforcement success.
type ClientAddressPolicy struct {
	Known   bool
	Allowed []string
	Denied  []string
	Reason  string
}

// ProposeClientAddressPolicy preserves older account-wide address cohorts and
// rejects newer ones beyond the cap. Equal starts at the cutoff are ambiguous:
// choosing lexicographically would arbitrarily disconnect a legitimate client.
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
		if observation.Starts[addresses[i]] != observation.Starts[addresses[j]] {
			return observation.Starts[addresses[i]] < observation.Starts[addresses[j]]
		}
		return addresses[i] < addresses[j]
	})
	if observation.Starts[addresses[limit-1]] == observation.Starts[addresses[limit]] {
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
