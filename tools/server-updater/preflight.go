package main

import (
	"context"
	"errors"
	"time"
)

// Read-only enrollment/apply readiness report. Touches no container: it is the
// only way an operator can confirm enrollment without performing an update.
func (c *Controller) preflightReport() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	active, op := c.active, c.state.Operation
	if active || c.blocked {
		return false, "busy"
	}
	if op != nil && op.Phase == "manual_recovery" {
		return false, "manual_recovery"
	}
	p, policyErr := c.authorizationPolicy()
	if policyErr != nil {
		return false, "not_approved"
	}
	var a Approval
	if p.Mode == "publisher_signed" {
		r, releaseErr := c.approvedPublisherRelease(p)
		if releaseErr != nil {
			return false, "not_approved"
		}
		a = r.approval
	} else {
		var approvalErr error
		a, approvalErr = c.approval()
		if approvalErr != nil {
			return false, "not_approved"
		}
	}
	if a.validateImages() != nil {
		return false, "not_approved"
	}
	if a.From != a.To {
		r, err := c.approvedRelease()
		if err != nil {
			return false, "not_approved"
		}
		a = r.approval
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := c.docker.preflight(ctx, a); err != nil {
		if errors.Is(err, errTopology) {
			return false, "topology_mismatch"
		}
		return false, "unavailable"
	}
	return true, ""
}
