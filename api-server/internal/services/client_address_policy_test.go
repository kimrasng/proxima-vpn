package services

import (
	"reflect"
	"testing"
)

func TestAddressPolicyRetainsOlderCohortsWithoutGuessingCutoff(t *testing.T) {
	obs := ClientAddressObservation{Complete: true, Addresses: []string{"203.0.113.4", "203.0.113.2", "203.0.113.3"}, Starts: map[string]int64{"203.0.113.2": 100, "203.0.113.3": 200, "203.0.113.4": 300}}
	got, err := ProposeClientAddressPolicy(obs, 1)
	if err != nil || !got.Known || !reflect.DeepEqual(got.Allowed, []string{"203.0.113.2"}) || !reflect.DeepEqual(got.Denied, []string{"203.0.113.3", "203.0.113.4"}) {
		t.Fatalf("policy=%+v err=%v", got, err)
	}
	obs.Starts["203.0.113.3"] = 100
	if got, err := ProposeClientAddressPolicy(obs, 1); err != nil || got.Known || got.Reason != "ambiguous_cutoff" {
		t.Fatalf("tie=%+v %v", got, err)
	}
	obs.Complete = false
	if got, _ := ProposeClientAddressPolicy(obs, 1); got.Known || len(got.Denied) != 0 {
		t.Fatal("partial evidence proposed eviction")
	}
}

func TestAddressPolicyAllowsWithinCapWithoutInventingStarts(t *testing.T) {
	obs := ClientAddressObservation{Complete: true, Addresses: []string{"203.0.113.3", "203.0.113.2"}}
	for _, limit := range []int{0, 2} {
		got, err := ProposeClientAddressPolicy(obs, limit)
		if err != nil || !got.Known || len(got.Allowed) != 2 || len(got.Denied) != 0 {
			t.Fatalf("limit=%d policy=%+v err=%v", limit, got, err)
		}
	}
	obs.Addresses = append(obs.Addresses, "203.0.113.2")
	if _, err := ProposeClientAddressPolicy(obs, 2); err == nil {
		t.Fatal("duplicate evidence accepted")
	}
}
