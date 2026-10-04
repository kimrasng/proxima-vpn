package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type fakePolicySource struct {
	relayRules []nodeprov.RelayRule
	exitRules  []nodeprov.ExitRule
	relayErr   error
	exitErr    error
	relayCalls int
	exitCalls  int
}

func (source *fakePolicySource) GetRelayRules(context.Context) ([]nodeprov.RelayRule, error) {
	source.relayCalls++
	return source.relayRules, source.relayErr
}

func (source *fakePolicySource) GetExitRules(context.Context) ([]nodeprov.ExitRule, error) {
	source.exitCalls++
	return source.exitRules, source.exitErr
}

type fakePolicyApplier struct {
	calls      int
	role       nodeprov.Role
	relayRules []nodeprov.RelayRule
	exitRules  []nodeprov.ExitRule
}

func (applier *fakePolicyApplier) Apply(role nodeprov.Role, relayRules []nodeprov.RelayRule, exitRules []nodeprov.ExitRule) error {
	applier.calls++
	applier.role = role
	applier.relayRules = relayRules
	applier.exitRules = exitRules
	return nil
}

func TestApplyRolePolicyDoesNotApplyOnFetchFailure(t *testing.T) {
	source := &fakePolicySource{exitErr: errors.New("control plane unavailable")}
	applier := &fakePolicyApplier{}
	err := applyRolePolicy(context.Background(), nodeprov.RoleExit, source, applier)

	if err == nil {
		t.Fatal("bootstrap succeeded after exit policy fetch failure")
	}
	if applier.calls != 0 {
		t.Errorf("partial policy applied %d time(s)", applier.calls)
	}
}

func TestApplyRolePolicyPreservesLastPolicyOnTransientFetchFailure(t *testing.T) {
	source := &fakePolicySource{
		exitRules: []nodeprov.ExitRule{{ExitPort: 443, Transport: nodeprov.TransportTCP}},
	}
	applier := &fakePolicyApplier{}
	if err := applyRolePolicy(context.Background(), nodeprov.RoleExit, source, applier); err != nil {
		t.Fatalf("initial apply: %v", err)
	}
	want := applier.exitRules
	source.exitErr = errors.New("temporary failure")

	err := applyRolePolicy(context.Background(), nodeprov.RoleExit, source, applier)

	if err == nil {
		t.Fatal("transient fetch failure was hidden")
	}
	if applier.calls != 1 {
		t.Errorf("policy applier called %d times, want only the successful apply", applier.calls)
	}
	if !reflect.DeepEqual(applier.exitRules, want) {
		t.Errorf("last policy changed after fetch failure: %+v", applier.exitRules)
	}
}

func TestApplyRolePolicyFetchesOnlyRulesRequiredByRole(t *testing.T) {
	tests := []struct {
		name      string
		role      nodeprov.Role
		wantRelay int
		wantExit  int
	}{
		{name: "relay", role: nodeprov.RoleRelay, wantRelay: 1},
		{name: "exit", role: nodeprov.RoleExit, wantExit: 1},
		{name: "both", role: nodeprov.RoleBoth, wantRelay: 1, wantExit: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &fakePolicySource{}
			applier := &fakePolicyApplier{}

			if err := applyRolePolicy(context.Background(), test.role, source, applier); err != nil {
				t.Fatalf("apply role policy: %v", err)
			}
			if source.relayCalls != test.wantRelay || source.exitCalls != test.wantExit {
				t.Errorf("fetch calls relay=%d exit=%d, want relay=%d exit=%d", source.relayCalls, source.exitCalls, test.wantRelay, test.wantExit)
			}
			if applier.calls != 1 || applier.role != test.role {
				t.Errorf("apply calls=%d role=%q, want one apply for %q", applier.calls, applier.role, test.role)
			}
		})
	}
}
