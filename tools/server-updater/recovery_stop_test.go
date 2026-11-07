package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Model the narrow immutable-ID stop boundary without changing shared fixtures.
type recoveryExecutor struct {
	*fakeExecutor
	stopFailure    string
	stopError      error
	keepRunning    bool
	replace        bool
	stoppedIDs     []string
	startUncertain bool
	startError     error
}

func (f *recoveryExecutor) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if binary == dockerBinary && len(args) == 5 && args[0] == "container" && args[1] == "stop" && args[2] == "--timeout" && args[3] == "20" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for service, info := range f.containers {
			if info.ID != args[4] {
				continue
			}
			f.stoppedIDs = append(f.stoppedIDs, info.ID)
			if service == f.stopFailure {
				return nil, f.stopError
			}
			if !f.keepRunning {
				info.State.Running, info.State.Restarting, info.State.Paused = false, false, false
			}
			if f.replace {
				info.ID = strings.Repeat("e", 64)
			}
			f.containers[service] = info
			return nil, nil
		}
		return nil, errors.New("container missing")
	}
	out, err := f.fakeExecutor.Run(ctx, binary, args...)
	if err == nil && f.startError != nil && binary == dockerBinary && len(args) > 9 && args[0] == "compose" && args[9] == "up" {
		return nil, f.startError
	}
	if err == nil && f.startUncertain && binary == dockerBinary && len(args) > 9 && args[0] == "compose" && args[9] == "up" {
		return nil, errUncertain
	}
	return out, err
}

func TestRecoveryStopsOnlyIdentifiedManagedContainers(t *testing.T) {
	for _, foreign := range []string{"none", "fingerprint", "project", "service", "duplicates", "restart_policy"} {
		t.Run(foreign, func(t *testing.T) {
			c, f, _ := fixture(t)
			info := f.containers["web"]
			switch foreign {
			case "fingerprint":
				info.Config.Labels[runtimeLabel] = "foreign"
			case "project":
				info.Config.Labels["com.docker.compose.project"] = "foreign"
			case "service":
				info.Config.Labels["com.docker.compose.service"] = "db"
			case "duplicates":
				info.ID += "\n" + strings.Repeat("c", 64)
			case "restart_policy":
				info.HostConfig.RestartPolicy.Name = "always"
			}
			f.containers["web"] = info
			recovery := &recoveryExecutor{fakeExecutor: f}
			c.docker.executor = recovery
			stopped := c.docker.stopManaged(context.Background())
			if stopped != (foreign == "none") || f.containers["api"].State.Running {
				t.Fatalf("stopped=%v api running=%v", stopped, f.containers["api"].State.Running)
			}
			if foreign == "none" {
				if f.containers["web"].State.Running || len(recovery.stoppedIDs) != 2 {
					t.Fatal("managed pair was not stopped")
				}
			} else if !f.containers["web"].State.Running || len(recovery.stoppedIDs) != 1 || recovery.stoppedIDs[0] != f.containers["api"].ID {
				t.Fatal("foreign or ambiguous service was mutated")
			}
			if f.unsafe {
				t.Fatal("unexpected Docker command")
			}
		})
	}
}

func TestRecoveryDoesNotMistakeStopAcknowledgementForStoppedPair(t *testing.T) {
	for _, failure := range []string{"error", "uncertain", "still_running", "replacement"} {
		t.Run(failure, func(t *testing.T) {
			c, f, _ := fixture(t)
			recovery := &recoveryExecutor{fakeExecutor: f}
			switch failure {
			case "error":
				recovery.stopFailure, recovery.stopError = "web", errors.New("stop failed")
			case "uncertain":
				recovery.stopFailure, recovery.stopError = "web", errUncertain
			case "still_running":
				recovery.keepRunning = true
			case "replacement":
				recovery.replace = true
			}
			c.docker.executor = recovery
			if c.docker.stopManaged(context.Background()) {
				t.Fatal("unverified shutdown reported as stopped")
			}
			if (failure == "error" || failure == "uncertain") && f.containers["api"].State.Running {
				t.Fatal("failed web stop prevented safe API stop")
			}
		})
	}
}
