package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalRenameFailureCannotHideRecoveryStops(t *testing.T) {
	for _, terminalPhase := range []string{"succeeded", "rolled_back"} {
		t.Run(terminalPhase, func(t *testing.T) {
			c, f, a := fixture(t)
			if terminalPhase == "rolled_back" {
				c.readiness = readyFunc(func(context.Context, string) error {
					if f.containers["api"].Config.Image == a.To.API {
						return errReadiness
					}
					return nil
				})
			}
			dir := c.store.dir
			postRenameFailed := false
			c.store.syncDir = func(path string) error {
				data, err := os.ReadFile(filepath.Join(dir, "state.json"))
				if err != nil {
					return err
				}
				var s State
				if err := json.Unmarshal(data, &s); err != nil {
					return err
				}
				if s.Operation.Phase != terminalPhase {
					return syncDirectory(path)
				}
				// The real rename has already installed terminal bytes. Fail the
				// directory sync syscall, then prevent later state writes before
				// rename. The independent marker must survive both failures.
				d, err := os.Open(path)
				if err != nil {
					return err
				}
				if err := d.Close(); err != nil {
					return err
				}
				postRenameFailed = true
				c.store.dir = filepath.Join(dir, "absent")
				return d.Sync()
			}
			if _, err := c.accept(a.ID); err != nil {
				t.Fatal(err)
			}
			waitOperation(t, c)
			if !postRenameFailed || f.containers["api"].State.Running || f.containers["web"].State.Running || f.unsafe {
				t.Fatal("post-rename failure was not contained")
			}
			loaded, err := (Store{dir: dir}).load()
			if err != nil || loaded.Operation.Phase != "manual_recovery" || loaded.CurrentKnown || !loaded.Operation.Maintenance || loaded.HighWater != 2 || len(loaded.Accepted) != 1 {
				t.Fatalf("restart trusted terminal state after recovery stops: %+v %v", loaded, err)
			}
			c.state, c.blocked = loaded, false
			if _, err := c.accept("next-release"); !errors.Is(err, errBusy) {
				t.Fatal("restart accepted another release", err)
			}
		})
	}
}

func TestRecoveryIntentFailurePreventsDockerMutation(t *testing.T) {
	c, f, a := fixture(t)
	c.store.syncDir = func(path string) error {
		if _, err := os.Stat(filepath.Join(path, "recovery-required")); err == nil {
			return errors.New("directory sync failed")
		}
		return syncDirectory(path)
	}
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s := waitOperation(t, c)
	if len(f.starts) != 0 || f.stops != 0 || !f.containers["api"].State.Running || !f.containers["web"].State.Running || !c.blocked || s.Operation.Phase != "manual_recovery" || f.unsafe {
		t.Fatal("mutated Docker without durable recovery intent")
	}
}

func TestRemovedIntentSyncFailureRequiresNewIntentBeforeStopping(t *testing.T) {
	c, f, a := fixture(t)
	dir := c.store.dir
	removed := false
	c.store.syncDir = func(path string) error {
		data, err := os.ReadFile(filepath.Join(path, "state.json"))
		if err != nil {
			return err
		}
		var state State
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
		_, markerErr := os.Stat(filepath.Join(path, "recovery-required"))
		if state.Operation.Phase == "succeeded" && errors.Is(markerErr, os.ErrNotExist) {
			removed = true
			c.store.dir = filepath.Join(dir, "absent")
			return errors.New("removal sync failed")
		}
		return syncDirectory(path)
	}
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s := waitOperation(t, c)
	if !removed || s.Operation.Phase != "manual_recovery" || !c.blocked || !f.containers["api"].State.Running || !f.containers["web"].State.Running || f.stops != 1 || f.unsafe {
		t.Fatal("cleanup changed runtime after losing durable intent")
	}
	loaded, err := (Store{dir: dir}).load()
	if err != nil || loaded.Operation.Phase != "succeeded" || !loaded.CurrentKnown {
		t.Fatalf("durable terminal state no longer matches untouched runtime: %+v %v", loaded, err)
	}
}
