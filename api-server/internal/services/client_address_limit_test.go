package services

import "testing"

func TestClientAddressLimitSelectsNewestOnlyFromCompleteEvidence(t *testing.T) {
	observation := ClientAddressObservation{Complete: true, Addresses: []string{"203.0.113.2", "203.0.113.3"}, Starts: map[string]int64{"203.0.113.2": 100, "203.0.113.3": 200}}
	got := DecideClientAddressLimit(observation, 1)
	if !got.Known || !got.OverLimit || got.Target != "203.0.113.3" {
		t.Fatalf("decision: %+v", got)
	}
	observation.Complete = false
	if got := DecideClientAddressLimit(observation, 1); got.Known || got.Target != "" {
		t.Fatalf("incomplete decision: %+v", got)
	}
	observation.Complete = true
	observation.Starts["203.0.113.2"] = 200
	if got := DecideClientAddressLimit(observation, 1); got.Target != "" || got.Reason != "ambiguous_newest" {
		t.Fatalf("tied decision: %+v", got)
	}
	delete(observation.Starts, "203.0.113.2")
	if got := DecideClientAddressLimit(observation, 1); got.Target != "" {
		t.Fatalf("unknown start picked target: %+v", got)
	}
}

func TestClientAddressLimitWithinCapAndUnlimitedDoNotEvict(t *testing.T) {
	observation := ClientAddressObservation{Complete: true, Addresses: []string{"203.0.113.2", "203.0.113.3"}}
	for _, cap := range []int{0, -1, 2, 3} {
		if got := DecideClientAddressLimit(observation, cap); !got.Known || got.OverLimit || got.Target != "" {
			t.Fatalf("cap=%d decision=%+v", cap, got)
		}
	}
}
