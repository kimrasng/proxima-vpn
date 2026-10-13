package services

// ClientAddressLimitDecision is a proposal, not proof of completed eviction.
// Unknown or tied starts suspend selection instead of choosing an arbitrary
// address. Nonpositive limits mean no address cap.
type ClientAddressLimitDecision struct {
	Known     bool
	OverLimit bool
	Target    string
	Reason    string
}

// DecideClientAddressLimit names the single address that
// ProposeClientAddressPolicy would deny first, so the read-only admin view and
// the opt-in enforcement path never disagree about who is next to go: an
// abandoned address (see staleAddressIdleMillis) before any active one, and
// otherwise the newest.
func DecideClientAddressLimit(observation ClientAddressObservation, limit int) ClientAddressLimitDecision {
	if !observation.Complete {
		return ClientAddressLimitDecision{Reason: "incomplete_observation"}
	}
	out := ClientAddressLimitDecision{Known: true}
	if limit <= 0 || len(observation.Addresses) <= limit {
		return out
	}
	out.OverLimit = true
	var (
		latest    int64
		latestOK  bool
		latestIsS bool
		tied      bool
	)
	seen := map[string]bool{}
	for _, address := range observation.Addresses {
		if seen[address] {
			return ClientAddressLimitDecision{Reason: "duplicate_address"}
		}
		seen[address] = true
		start, exists := observation.Starts[address]
		if !exists || start <= 0 {
			out.Target = ""
			out.Reason = "unknown_start"
			return out
		}
		stale := addressIsStale(observation, address)
		switch {
		case !latestOK, stale && !latestIsS, stale == latestIsS && start > latest:
			latest, latestOK, latestIsS = start, true, stale
			out.Target = address
			tied = false
		case stale == latestIsS && start == latest:
			tied = true
		}
	}
	if tied {
		out.Target = ""
		out.Reason = "ambiguous_newest"
	}
	return out
}
