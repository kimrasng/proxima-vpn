package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparationBlockedByRuntimeAndRecoveryGates(t *testing.T) {
	for _, kind := range []string{"active", "blocked", "unknown current", "legacy", "recovery marker", "mutex"} {
		t.Run(kind, func(t *testing.T) {
			c, e, _ := publisherFixture(t)
			switch kind {
			case "active":
				c.active = true
			case "blocked":
				c.blocked = true
			case "unknown current":
				c.state.CurrentKnown = false
			case "legacy":
				c.state = State{Version: 1}
			case "recovery marker":
				if err := c.store.requireRecovery(); err != nil {
					t.Fatal(err)
				}
			case "mutex":
				c.mu.Lock()
				defer c.mu.Unlock()
			}
			if _, err := c.startPreparation("v1"); !errors.Is(err, errBusy) || len(e.pulled) != 0 {
				t.Fatal("preparation bypassed gate", err)
			}
		})
	}
}

func TestPreparedEnvelopeRejectsSymlinkAndWritableFile(t *testing.T) {
	for _, kind := range []string{"symlink", "writable", "oversized", "directory"} {
		t.Run(kind, func(t *testing.T) {
			c, e, a := publisherFixture(t)
			candidate := completePreparation(t, c)
			path := filepath.Join(c.store.dir, preparedEnvelopeName)
			switch kind {
			case "symlink":
				other := filepath.Join(c.store.dir, "other.json")
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(path, 0660); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", 16385)), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.acceptCandidate(a.ID, candidate.PayloadSHA256); !errors.Is(err, errApproval) || len(e.base.starts) != 0 {
				t.Fatal("unsafe candidate accepted", err)
			}
		})
	}
}

func TestPreparationShutdownCancelsDetachedFetch(t *testing.T) {
	c, e, _ := publisherFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.ctx = ctx
	entered := make(chan struct{})
	c.fetch = func(ctx context.Context, _ string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Minute {
			t.Error("preparation lacks bounded context")
		}
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if _, err := c.startPreparation("v1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch not entered")
	}
	cancel()
	waitOperation(t, c)
	if c.preparationStatus().Status != "failed" || c.active || len(e.pulled) != 0 {
		t.Fatal("canceled preparation continued")
	}
}

func TestPublisherApplyRechecksRootOptIn(t *testing.T) {
	c, e, a := publisherFixture(t)
	candidate := completePreparation(t, c)
	c.authorization = func() (authorizationPolicy, error) { return authorizationPolicy{Mode: "manual"}, nil }
	if _, err := c.acceptCandidate(a.ID, candidate.PayloadSHA256); err == nil || len(e.base.starts) != 0 {
		t.Fatal("revoked delegation still applied")
	}
	if _, err := c.accept(a.ID); err == nil || len(e.base.starts) != 0 {
		t.Fatal("manual mode fell back to publisher candidate")
	}
}
