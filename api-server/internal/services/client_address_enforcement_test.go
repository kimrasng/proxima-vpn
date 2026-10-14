package services

import (
	"os"
	"testing"
)

func TestClientAddressEnforcementIsOnByDefault(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", true}, {"1", true}, {"true", true}, {"on", true}, {"yes", true},
		{"enabled", true}, {"disable", true}, {"flase", true}, // a typo must not turn the limit off
		{"0", false}, {"false", false}, {"FALSE", false}, {"off", false}, {"Off", false}, {"no", false},
		{" 0 ", false}, {"\tfalse\n", false},
	} {
		t.Setenv(ClientAddressLimitEnvVar, tc.value)
		if got := ClientAddressEnforcementEnabled(); got != tc.want {
			t.Errorf("%s=%q: enabled=%v, want %v", ClientAddressLimitEnvVar, tc.value, got, tc.want)
		}
	}
}

func TestClientAddressEnforcementUnsetIsOn(t *testing.T) {
	// t.Setenv("") sets the variable to empty; make sure a truly absent
	// variable behaves the same way.
	t.Setenv(ClientAddressLimitEnvVar, "x") // registers restore-on-cleanup
	if err := os.Unsetenv(ClientAddressLimitEnvVar); err != nil {
		t.Fatal(err)
	}
	if !ClientAddressEnforcementEnabled() {
		t.Fatal("absent variable must leave enforcement on")
	}
}
