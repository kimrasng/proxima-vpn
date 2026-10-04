package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/relay"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type policySource interface {
	GetRelayRules(context.Context) ([]nodeprov.RelayRule, error)
	GetExitRules(context.Context) ([]nodeprov.ExitRule, error)
}

type policyApplier interface {
	Apply(nodeprov.Role, []nodeprov.RelayRule, []nodeprov.ExitRule) error
}

func applyRolePolicy(ctx context.Context, role nodeprov.Role, source policySource, applier policyApplier) error {
	if !role.Valid() {
		return fmt.Errorf("unknown role %q", role)
	}
	var relayRules []nodeprov.RelayRule
	var exitRules []nodeprov.ExitRule
	var err error
	if role.Forwards() {
		relayRules, err = source.GetRelayRules(ctx)
		if err != nil {
			return fmt.Errorf("fetch relay rules: %w", err)
		}
	}
	if role.Exits() {
		exitRules, err = source.GetExitRules(ctx)
		if err != nil {
			return fmt.Errorf("fetch exit rules: %w", err)
		}
	}
	return applier.Apply(role, relayRules, exitRules)
}

func waitForPolicy(ctx context.Context, role nodeprov.Role, source policySource, manager *relay.Manager) error {
	backoff := time.Second
	for {
		if err := applyRolePolicy(ctx, role, source, manager); err == nil {
			return nil
		} else {
			log.Printf("node policy bootstrap: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func policyPollLoop(ctx context.Context, role nodeprov.Role, source policySource, manager *relay.Manager) {
	ticker := time.NewTicker(relayRulePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := applyRolePolicy(ctx, role, source, manager); err != nil {
				log.Printf("node policy poll: %v", err)
			}
		}
	}
}
