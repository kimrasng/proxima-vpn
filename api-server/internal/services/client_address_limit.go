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

func DecideClientAddressLimit(observation ClientAddressObservation, limit int) ClientAddressLimitDecision {
	if !observation.Complete {
		return ClientAddressLimitDecision{Reason: "incomplete_observation"}
	}
	out := ClientAddressLimitDecision{Known: true}
	if limit <= 0 || len(observation.Addresses) <= limit {
		return out
	}
	out.OverLimit = true
	var latest int64
	tied := false
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
		if start > latest {
			latest = start
			out.Target = address
			tied = false
		} else if start == latest {
			tied = true
		}
	}
	if tied {
		out.Target = ""
		out.Reason = "ambiguous_newest"
	}
	return out
}
