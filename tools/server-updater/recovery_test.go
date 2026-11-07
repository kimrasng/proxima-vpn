package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFailedDockerStartNeverAuthorizesRollback(t *testing.T) {
	for _, failure := range []error{errors.New("exit status 1"), io.EOF, syscall.ECONNRESET, errors.New("signal: killed"), errReadiness} {
		t.Run(failure.Error(), func(t *testing.T) {
			c, f, a := fixture(t)
			c.docker.executor = &recoveryExecutor{fakeExecutor: f, startError: failure}
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			if s.Operation.Phase != "manual_recovery" || !s.Operation.Maintenance || s.CurrentKnown || len(f.starts) != 1 || f.containers["api"].State.Running || f.containers["web"].State.Running || f.unsafe {
				t.Fatalf("failed Docker command authorized rollback: %+v starts=%v", s.Operation, f.starts)
			}
		})
	}
}

func TestRestartContainsInterruptedPairWithoutResuming(t *testing.T) {
	for _, phase := range []string{"accepted", "stopping", "applying", "rolling_back", "manual_recovery"} {
		t.Run(phase, func(t *testing.T) {
			c, f, a := fixture(t)
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			s.Operation.Phase = phase
			s.Current, s.CurrentKnown = a.From, phase == "accepted"
			if err := c.store.save(s); err != nil {
				t.Fatal(err)
			}
			loaded, err := c.store.load()
			if err != nil {
				t.Fatal(err)
			}
			c.state = loaded
			starts := len(f.starts)
			c.recover()
			persisted, err := c.store.load()
			if err != nil || persisted.Operation.Phase != "manual_recovery" || !persisted.Operation.Maintenance || persisted.CurrentKnown || persisted.HighWater != s.HighWater || len(persisted.Accepted) != len(s.Accepted) {
				t.Fatalf("recovery lost gate or ledger: %+v %v", persisted, err)
			}
			if f.containers["api"].State.Running || f.containers["web"].State.Running || len(f.starts) != starts || f.unsafe {
				t.Fatal("restart did not contain pair without starting containers")
			}
			if _, err := c.accept("another-release"); !errors.Is(err, errBusy) {
				t.Fatal("restart cleared recovery gate", err)
			}
		})
	}
}

func TestUncertainApplyRemainsGatedAfterContainment(t *testing.T) {
	c, f, a := fixture(t)
	c.docker.executor = &recoveryExecutor{fakeExecutor: f, startUncertain: true}
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s := waitOperation(t, c)
	if s.Operation.Phase != "manual_recovery" || !s.Operation.Maintenance || s.CurrentKnown || len(f.starts) != 1 || f.containers["api"].State.Running || f.containers["web"].State.Running || f.unsafe {
		t.Fatalf("uncertain activation was retried or left running: %+v", s)
	}
	// A Docker request can finish after the stopped snapshot. It must not
	// turn that snapshot into authorization for rollback or another release.
	f.install("api", a.To.API)
	if _, err := c.accept("another-release"); !errors.Is(err, errBusy) {
		t.Fatal("late Docker outcome cleared the gate", err)
	}
	if ok, _ := c.preflightReport(); ok || len(f.starts) != 1 {
		t.Fatal("uncertain operation authorized more work")
	}
}

func TestRecoveryStopFailureRemainsDurablyVisible(t *testing.T) {
	c, f, a := fixture(t)
	c.docker.executor = &recoveryExecutor{fakeExecutor: f, stopFailure: "web", stopError: errUncertain}
	a.RollbackCompatible = false
	c.approval = func() (Approval, error) { return a, nil }
	c.readiness = readyFunc(func(context.Context, string) error { return errReadiness })
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	waitOperation(t, c)
	s, err := c.store.load()
	if err != nil || s.Operation.Error != "recovery_stop_unverified" || s.Operation.Phase != "manual_recovery" || s.CurrentKnown || !s.Operation.Maintenance || f.containers["api"].State.Running || f.unsafe {
		t.Fatalf("unverified containment hidden: %+v %v", s, err)
	}
}

// These cases fail after containers have actually started. The older outcome
// tests fail before create, so a recovery label alone could satisfy them.
func TestManualRecoveryStopsStartedPair(t *testing.T) {
	for _, failure := range []string{"incompatible_readiness", "rollback_readiness", "cancelled", "journal"} {
		t.Run(failure, func(t *testing.T) {
			c, f, a := fixture(t)
			c.docker.executor = &recoveryExecutor{fakeExecutor: f}
			a.RollbackCompatible = failure == "rollback_readiness"
			c.approval = func() (Approval, error) { return a, nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.ctx = ctx
			c.readiness = readyFunc(func(_ context.Context, service string) error {
				if service != "web" {
					return nil
				}
				switch failure {
				case "cancelled":
					cancel()
					return context.Canceled
				case "journal":
					path := filepath.Join(c.store.dir, "state.json")
					if err := os.Rename(path, path+".saved"); err != nil {
						return err
					}
					if err := os.Mkdir(path, 0700); err != nil {
						return err
					}
					return nil
				default:
					return errReadiness
				}
			})
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			if s.Operation.Phase != "manual_recovery" {
				t.Fatalf("expected manual recovery, got %+v", s.Operation)
			}
			for _, service := range []string{"api", "web"} {
				if f.containers[service].State.Running || f.containers[service].State.Restarting {
					t.Errorf("%s still serves after %s", service, failure)
				}
			}
			if f.unsafe {
				t.Fatal("recovery exceeded the Docker command boundary")
			}
		})
	}
}
