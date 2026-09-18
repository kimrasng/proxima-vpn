package scheduler

import "testing"

func TestEvictionTargetsTheDeviceSpreadAcrossMostAddresses(t *testing.T) {
	perDevice := map[string]map[string]int64{
		"device-shared": {
			"203.0.113.1": 1000,
			"203.0.113.2": 1001,
			"203.0.113.3": 1002,
		},
		"device-honest": {
			"198.51.100.9": 5000,
		},
	}

	if got := pickEvictionTarget(perDevice); got != "device-shared" {
		t.Fatalf("evicted %q; the shared credential is device-shared", got)
	}
}

// The honest device here has the newest activity, which must not save the
// credential that is spread across several machines.
func TestRecentActivityDoesNotOutweighAddressSpread(t *testing.T) {
	perDevice := map[string]map[string]int64{
		"device-shared": {"203.0.113.1": 10, "203.0.113.2": 11},
		"device-honest": {"198.51.100.9": 999999},
	}

	if got := pickEvictionTarget(perDevice); got != "device-shared" {
		t.Fatalf("evicted %q, want device-shared", got)
	}
}

func TestEqualSpreadEvictsTheMostRecentlySeen(t *testing.T) {
	perDevice := map[string]map[string]int64{
		"device-established": {"203.0.113.1": 100},
		"device-newcomer":    {"198.51.100.9": 900},
	}

	if got := pickEvictionTarget(perDevice); got != "device-newcomer" {
		t.Fatalf("evicted %q; an established session outranks the newcomer", got)
	}
}

func TestEvictionTargetIsStableForIdenticalCandidates(t *testing.T) {
	perDevice := map[string]map[string]int64{
		"aaa": {"203.0.113.1": 100},
		"bbb": {"198.51.100.9": 100},
	}

	first := pickEvictionTarget(perDevice)
	for i := 0; i < 50; i++ {
		if got := pickEvictionTarget(perDevice); got != first {
			t.Fatalf("target flipped between %q and %q across identical inputs", first, got)
		}
	}
}

func TestNoOnlineDevicesYieldsNoTarget(t *testing.T) {
	if got := pickEvictionTarget(map[string]map[string]int64{}); got != "" {
		t.Fatalf("returned %q with nothing online", got)
	}
}
