package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSignedRollbackAndRootReconciliationPreserveReplayLedger(t *testing.T) {
	c, f, a := fixture(t)
	c.readiness = readyFunc(func(context.Context, string) error {
		if f.containers["api"].Config.Image == a.To.API {
			return errReadiness
		}
		return nil
	})
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s := waitOperation(t, c)
	if s.Operation.Phase != "rolled_back" || s.HighWater != 2 || s.Current != a.From || !s.CurrentKnown {
		t.Fatalf("rollback lost ledger: %+v", s)
	}
	// Root may reconcile the operation, not erase accepted identities.
	s.Operation = nil
	if err := c.store.save(s); err != nil {
		t.Fatal(err)
	}
	var err error
	c.state, err = c.store.load()
	if err != nil {
		t.Fatal(err)
	}
	a.ID = "retry-release"
	c.approval = func() (Approval, error) { return a, nil }
	input, _ := signedFixtureRelease(t, a, 2)
	c.release = func() (releaseInput, error) { return input, nil }
	if _, err := c.accept(a.ID); !errors.Is(err, errApproval) {
		t.Fatal("consumed sequence accepted", err)
	}
	if c.state.HighWater != 2 || len(c.state.Accepted) != 1 {
		t.Fatal("rejection changed ledger")
	}
	input, _ = signedFixtureRelease(t, a, 3)
	c.readiness = readyFunc(func(context.Context, string) error { return nil })
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s = waitOperation(t, c)
	if s.Operation.Phase != "succeeded" || s.HighWater != 3 || len(s.Accepted) != 2 || s.Accepted[0].Approval.ID != "release-one" || s.Current != a.To {
		t.Fatalf("fresh retry failed: %+v", s)
	}
}

func TestSignedCrashRetainsAcceptedIdentityAndBlocksExecution(t *testing.T) {
	for _, phase := range []string{"accepted", "stopping", "applying", "rolling_back"} {
		t.Run(phase, func(t *testing.T) {
			c, f, a := fixture(t)
			// Capture the controller-produced accepted transaction before execution.
			c.docker.executor = interruptedExecutor{}
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			waitOperation(t, c)
			s := c.snapshot()
			s.Operation.Phase = phase
			s.CurrentKnown = phase == "accepted"
			if err := c.store.save(s); err != nil {
				t.Fatal(err)
			}
			loaded, err := c.store.load()
			if err != nil {
				t.Fatal(err)
			}
			c.state = loaded
			if loaded.Operation.Phase != "manual_recovery" || loaded.CurrentKnown || loaded.HighWater != 2 || loaded.Operation.PayloadSHA256 != s.Operation.PayloadSHA256 || len(loaded.Accepted) != 1 {
				t.Fatalf("crash lost acceptance: %+v", loaded)
			}
			if _, err := c.accept("next-release"); !errors.Is(err, errBusy) || f.stops != 0 || len(f.starts) != 0 {
				t.Fatal("crash resumed execution", err)
			}
		})
	}
}

type interruptedExecutor struct{}

func (interruptedExecutor) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errUncertain
}

func TestSignedJournalFailureLatchesUntilRestart(t *testing.T) {
	c, f, a := fixture(t)
	dir := c.store.dir
	c.store.dir = filepath.Join(dir, "absent")
	if _, err := c.accept(a.ID); err == nil {
		t.Fatal("accepted without journal")
	}
	c.store.dir = dir
	if _, err := c.accept(a.ID); !errors.Is(err, errBusy) {
		t.Fatal("retried uncertain journal write", err)
	}
	if f.stops != 0 || len(f.starts) != 0 || c.active || c.state.HighWater != 1 {
		t.Fatal("journal failure mutated runtime")
	}
}

func TestSignedStoreRejectsCorruptLedgerAndMissingBaseline(t *testing.T) {
	for _, kind := range []string{"missing", "legacy", "zero", "missing_policy", "wrong_product", "duplicate_id", "missing_digest", "wrong_high_water", "missing_history", "known_recovery"} {
		t.Run(kind, func(t *testing.T) {
			c, _, a := fixture(t)
			if kind == "missing" || kind == "legacy" {
				if kind == "legacy" {
					if err := os.WriteFile(filepath.Join(c.store.dir, "state.json"), []byte(`{"version":1,"consumed":[],"operation":null}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				s, err := c.store.load()
				if err != nil {
					t.Fatal(err)
				}
				c.state = s
				if _, err := c.accept(a.ID); !errors.Is(err, errApproval) {
					t.Fatal("unsigned baseline accepted", err)
				}
				return
			}
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			s := waitOperation(t, c)
			switch kind {
			case "zero":
				s.HighWater = 0
			case "missing_policy":
				s.Policy.PublicKey = ""
			case "wrong_product":
				s.Product = "other"
			case "duplicate_id":
				s.Consumed = append(s.Consumed, s.Consumed[0])
				s.Accepted = append(s.Accepted, s.Accepted[0])
			case "missing_digest":
				s.Accepted[0].PayloadSHA256 = ""
			case "wrong_high_water":
				s.HighWater++
			case "missing_history":
				s.Accepted = nil
			case "known_recovery":
				s.Operation.Phase = "manual_recovery"
			}
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(c.store.dir, "state.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := c.store.load(); err == nil {
				t.Fatal("corrupt signed journal accepted")
			}
		})
	}
}

func TestSignedPreflightRequiresManifestExceptEqualPairEnrollment(t *testing.T) {
	c, f, a := fixture(t)
	c.release = func() (releaseInput, error) { return releaseInput{}, os.ErrNotExist }
	if ok, reason := c.preflightReport(); ok || reason != "not_approved" {
		t.Fatal("approval-only update preflight passed")
	}
	a.To = a.From
	c.approval = func() (Approval, error) { return a, nil }
	if ok, reason := c.preflightReport(); !ok || reason != "" {
		t.Fatal("read-only bootstrap rejected", reason)
	}
	if f.stops != 0 || len(f.starts) != 0 || c.state.HighWater != 1 {
		t.Fatal("preflight consumed release")
	}
}
