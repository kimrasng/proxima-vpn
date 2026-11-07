package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

var errBusy = errors.New("controller blocked")
var errApproval = errors.New("approval unavailable")

type Controller struct {
	mu               sync.Mutex
	state            State
	store            Store
	docker           Docker
	readiness        Readiness
	approval         func() (Approval, error)
	release          func() (releaseInput, error)
	now              func() time.Time
	blocked          bool
	active           bool
	done             chan struct{}
	ctx              context.Context
	recoveryRequired bool
	authorization    func() (authorizationPolicy, error)
	policy           func() (releaseverify.Policy, error)
	fetch            func(context.Context, string) ([]byte, error)
	preparing        bool
	preparation      preparationResponse
}

func (c *Controller) snapshot() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Consumed = append([]string{}, s.Consumed...)
	s.Accepted = append([]AcceptedRelease{}, s.Accepted...)
	if s.Operation != nil {
		op := *s.Operation
		s.Operation = &op
	}
	return s
}

func (c *Controller) accept(id string) (State, error) {
	return c.acceptCandidate(id, "")
}

func (c *Controller) acceptCandidate(id, payloadHash string) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked || c.preparing {
		return State{}, errBusy
	}
	policy, err := c.authorizationPolicy()
	if err != nil || (policy.Mode == "publisher_signed" && !imageID.MatchString("sha256:"+payloadHash)) {
		return State{}, errApproval
	}
	if policy.Mode == "manual" && payloadHash != "" {
		return State{}, errInvalid
	}
	if c.state.Operation != nil && c.state.Operation.Approval.ID == id {
		if payloadHash != "" && payloadHash != c.state.Operation.PayloadSHA256 {
			return State{}, errApproval
		}
		return c.state, nil
	}
	if c.active || (c.state.Operation != nil && c.state.Operation.Phase == "manual_recovery") {
		return State{}, errBusy
	}
	for _, used := range c.state.Consumed {
		if used == id {
			return State{}, errApproval
		}
	}
	if len(c.state.Consumed) >= 10000 {
		return State{}, errBusy
	}
	r, err := c.approvedRelease()
	if err != nil || r.approval.ID != id {
		return State{}, errApproval
	}
	currentPolicy, err := c.authorizationPolicy()
	if err != nil || currentPolicy != policy {
		return State{}, errApproval
	}
	a := r.approval
	if err := a.validate(); err != nil {
		return State{}, errApproval
	}
	m, digest := r.verified.Manifest(), r.verified.PayloadSHA256()
	if payloadHash != "" && payloadHash != hex.EncodeToString(digest[:]) {
		return State{}, errApproval
	}
	s := c.state
	s.HighWater = m.Sequence
	s.Consumed = append(append([]string{}, c.state.Consumed...), id)
	s.Operation = &Operation{Approval: a, Phase: "accepted", Sequence: m.Sequence, PayloadSHA256: hex.EncodeToString(digest[:])}
	s.Accepted = append(append([]AcceptedRelease{}, c.state.Accepted...), AcceptedRelease{a, m.Sequence, s.Operation.PayloadSHA256})
	if err := c.store.save(s); err != nil {
		// Rename may have succeeded before directory fsync failed. Never
		// overwrite that possibly committed acceptance from stale memory.
		c.blocked = true
		return State{}, err
	}
	c.state = s
	c.active = true
	c.done = make(chan struct{})
	go func() {
		defer func() { c.mu.Lock(); c.active = false; close(c.done); c.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(c.ctx, 15*time.Minute)
		defer cancel()
		if err := c.execute(ctx, a); err != nil {
			// The write-ahead marker survives a terminal rename followed by a
			// failed directory sync. Block memory as well as restart recovery.
			c.mu.Lock()
			op := *c.state.Operation
			op.Phase, op.Error, op.Maintenance = "manual_recovery", "persistence_failure", true
			c.state.Operation = &op
			c.state.CurrentKnown = false
			c.blocked = true
			c.mu.Unlock()
		}
		c.recover()
	}()
	return s, nil
}

func (c *Controller) phase(phase string, maintenance bool, uncertain bool, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	op := *c.state.Operation
	op.Phase, op.Maintenance, op.Error = phase, maintenance, code
	op.MigrationUncertain = op.MigrationUncertain || uncertain
	s := c.state
	s.Operation = &op
	switch phase {
	case "succeeded":
		s.Current, s.CurrentKnown = op.Approval.To, true
	case "rolled_back":
		s.Current, s.CurrentKnown = op.Approval.From, true
	default:
		// Even preflight rejection may mean the runtime pair drifted.
		s.CurrentKnown = false
	}
	if err := c.store.save(s); err != nil {
		return err
	}
	c.state = s
	return nil
}

func (c *Controller) execute(ctx context.Context, a Approval) error {
	if err := c.docker.preflight(ctx, a); err != nil {
		return c.phase("rejected", false, false, "preflight_failed")
	}
	if err := c.docker.writeRuntime(a.From); err != nil {
		return err
	}
	if err := c.store.requireRecovery(); err != nil {
		return err
	}
	c.recoveryRequired = true
	if err := c.phase("stopping", true, false, ""); err != nil {
		return err
	}
	if err := c.docker.stop(ctx); err != nil {
		return c.phase("manual_recovery", true, false, "stop_failed")
	}
	if err := c.docker.verify(ctx, a.From, false); err != nil {
		return c.phase("manual_recovery", true, false, "stop_unverified")
	}
	if err := c.docker.writeRuntime(a.To); err != nil {
		return err
	}
	if err := c.phase("applying", true, true, ""); err != nil {
		return err
	}
	err := c.activate(ctx, a.To)
	if err == nil {
		return c.finish("succeeded", "")
	}
	if !errors.Is(err, errReadiness) || errors.Is(err, errUncertain) || ctx.Err() != nil || !a.RollbackCompatible {
		return c.phase("manual_recovery", true, true, "apply_failed")
	}
	if err := c.phase("rolling_back", true, true, "apply_failed"); err != nil {
		return err
	}
	if err := c.docker.stop(ctx); err != nil {
		return c.phase("manual_recovery", true, true, "rollback_stop_failed")
	}
	// A readiness failure may leave mixed source/target containers; confirm all
	// stopped before any rollback starts without assuming which image exists.
	for _, service := range []string{"api", "web"} {
		info, err := c.docker.inspect(ctx, service)
		if err != nil || info.State.Running || info.State.Restarting {
			return c.phase("manual_recovery", true, true, "rollback_stop_unverified")
		}
	}
	if err := c.docker.writeRuntime(a.From); err != nil {
		return err
	}
	if err := c.activate(ctx, a.From); err != nil {
		return c.phase("manual_recovery", true, true, "rollback_failed")
	}
	return c.finish("rolled_back", "apply_failed")
}

func (c *Controller) activate(ctx context.Context, images Images) error {
	for _, service := range []string{"api", "web"} {
		if err := c.docker.start(ctx, service); err != nil {
			return err
		}
		if err := c.readiness.Wait(ctx, service); err != nil {
			return err
		}
	}
	if err := c.docker.verify(ctx, images, true); err != nil {
		return fmt.Errorf("%w: identity", errUncertain)
	}
	return nil
}

// Called only under c.mu. Neither socket input nor the manifest supplies the
// clock, trust policy, baseline images, or replay high-water.
func (c *Controller) approvedRelease() (approvedRelease, error) {
	p, err := c.authorizationPolicy()
	if err != nil {
		return approvedRelease{}, errApproval
	}
	if p.Mode == "publisher_signed" {
		return c.approvedPublisherRelease(p)
	}
	if c.state.validateSigned() != nil || !c.state.CurrentKnown || c.release == nil || c.now == nil || c.approval == nil {
		return approvedRelease{}, errApproval
	}
	input, err := c.release()
	if err != nil || input.policy != c.state.Policy {
		return approvedRelease{}, errApproval
	}
	a, err := c.approval()
	if err != nil {
		return approvedRelease{}, err
	}
	return verifyRelease(input, a, releaseverify.Context{HighWater: c.state.HighWater, Current: releaseverify.Images(c.state.Current), Now: c.now()})
}
