package scheduler

import "testing"

func TestNewestSessionWinsRegardlessOfAddressActivity(t *testing.T) {
	starts := map[string]int64{"established": 100, "newest": 200, "other": 150}
	if got := pickEvictionTarget(starts); got != "newest" {
		t.Fatalf("target=%s", got)
	}
}
func TestUnknownStartCannotBeSelected(t *testing.T) {
	if got := pickEvictionTarget(nil); got != "" {
		t.Fatalf("target=%s", got)
	}
}
func TestTieIsStable(t *testing.T) {
	if got := pickEvictionTarget(map[string]int64{"bbb": 10, "aaa": 10}); got != "" {
		t.Fatalf("ambiguous same-second arrival must not select a victim: %s", got)
	}
}
func TestEnforcementFlagRequiresExplicitOptInAndFleetChecks(t *testing.T) {
	t.Setenv("ENFORCE_CONCURRENCY", "")
	if NewConcurrencyScheduler(nil, nil).enforce {
		t.Fatal("default must observe")
	}
	t.Setenv("ENFORCE_CONCURRENCY", "1")
	s := NewConcurrencyScheduler(nil, nil)
	if !s.enforce {
		t.Fatal("explicit opt-in did not activate guarded enforcement")
	}
}

func TestConcurrencyDefaultsAndOverrides(t *testing.T) {
	t.Setenv("CONCURRENCY_GRACE", "")
	t.Setenv("CONCURRENCY_STRIKES", "")
	if nonnegativeEnv("CONCURRENCY_GRACE", 0) != 0 || positiveEnv("CONCURRENCY_STRIKES", 2) != 2 {
		t.Fatal("defaults")
	}
	t.Setenv("CONCURRENCY_GRACE", "1")
	t.Setenv("CONCURRENCY_STRIKES", "3")
	if nonnegativeEnv("CONCURRENCY_GRACE", 0) != 1 || positiveEnv("CONCURRENCY_STRIKES", 2) != 3 {
		t.Fatal("overrides")
	}
}
