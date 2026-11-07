package main

import (
	"context"
	"time"
)

// recover never starts a container or clears the recovery gate. In particular,
// a stopped snapshot cannot settle a create/start request left in Docker after
// a timeout or controller crash. Root must reconcile those requests as well.
func (c *Controller) recover() {
	s := c.snapshot()
	if s.Operation == nil || s.Operation.Phase != "manual_recovery" {
		return
	}
	// No mutation without durable intent independent of state.json. A failed
	// terminal rename/sync must not erase the authorization for containment.
	if !c.recoveryRequired {
		if err := c.store.requireRecovery(); err != nil {
			c.mu.Lock()
			c.blocked = true
			c.mu.Unlock()
			return
		}
		c.recoveryRequired = true
	}
	if err := c.phase("manual_recovery", true, false, s.Operation.Error); err != nil {
		c.mu.Lock()
		c.blocked = true
		c.mu.Unlock()
	}
	// Shutdown cancels the operation context. Containment gets its own bounded
	// budget below systemd's TimeoutStopSec, rather than inheriting cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	if !c.docker.stopManaged(ctx) {
		if err := c.phase("manual_recovery", true, false, "recovery_stop_unverified"); err != nil {
			c.mu.Lock()
			op := *c.state.Operation
			op.Error = "recovery_stop_unverified"
			c.state.Operation = &op
			c.blocked = true
			c.mu.Unlock()
		}
	}
}

// stopManaged only targets immutable IDs positively identified as belonging to
// this runtime. A missing, duplicated or foreign service does not authorize a
// broad Compose stop, and does not prevent stopping the other managed service.
// The result is a snapshot of this pair, never proof that an outstanding Docker
// create/start request cannot complete later.
func (d Docker) stopManaged(ctx context.Context) bool {
	ids := make(map[string]string, 2)
	stopped := true
	for _, service := range []string{"web", "api"} {
		info, err := d.inspect(ctx, service)
		if err != nil || !d.managedRecoveryContainer(info, service) {
			stopped = false
			continue
		}
		ids[service] = info.ID
		if _, err := d.executor.Run(ctx, dockerBinary, "container", "stop", "--timeout", "20", info.ID); err != nil {
			// An unsuccessful stop acknowledgement is not durable evidence that
			// Docker suppressed its restart policy, even if inspect says exited.
			stopped = false
		}
	}
	for _, service := range []string{"web", "api"} {
		info, err := d.inspect(ctx, service)
		if err != nil || ids[service] == "" || info.ID != ids[service] ||
			!d.managedRecoveryContainer(info, service) || info.State.Running || info.State.Restarting || info.State.Paused {
			stopped = false
		}
	}
	return stopped
}

func (d Docker) managedRecoveryContainer(info containerInfo, service string) bool {
	return containerID.MatchString(info.ID) &&
		info.Config.Labels["com.docker.compose.project"] == d.config.Project &&
		info.Config.Labels["com.docker.compose.service"] == service &&
		info.Config.Labels[runtimeLabel] == d.config.fingerprint() &&
		info.HostConfig.RestartPolicy.Name == "unless-stopped"
}
