package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Pull support belongs only to the host preparation executor, not the daemon's
// fake allowlist. Unknown refs and every container mutation still fail closed.
type preparationExecutor struct {
	base      *fakeExecutor
	approved  Images
	pulled    []string
	fail      string
	afterPull func()
}

func (e *preparationExecutor) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if len(args) == 3 && binary == dockerBinary && args[0] == "image" && args[1] == "pull" {
		ref := args[2]
		if ref != e.approved.API && ref != e.approved.Web {
			return nil, errInvalid
		}
		e.pulled = append(e.pulled, ref)
		if ref == e.fail {
			return nil, errUncertain
		}
		e.base.images[ref] = "sha256:" + strings.Repeat("e", 64)
		if e.afterPull != nil {
			e.afterPull()
		}
		return nil, nil
	}
	return e.base.Run(ctx, binary, args...)
}

func TestPreparationCachesApprovedImagesWithoutAcceptingOrStopping(t *testing.T) {
	c, f, a := fixture(t)
	input, _ := c.release()
	before := c.snapshot()
	delete(f.images, a.To.API)
	delete(f.images, a.To.Web)
	e := &preparationExecutor{base: f, approved: a.To}
	c.docker.executor = e
	candidate, err := prepareImages(context.Background(), c.docker, input, a, c.state, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Status != "prepared" || candidate.ReleaseID != a.ID || candidate.Sequence != 2 || candidate.To != a.To || len(candidate.PayloadSHA256) != 64 {
		t.Fatalf("incorrect candidate: %+v", candidate)
	}
	if !reflect.DeepEqual(e.pulled, []string{a.To.API, a.To.Web}) || f.images[a.To.API] == "" || f.images[a.To.Web] == "" {
		t.Fatal("approved digest pair not cached")
	}
	if !reflect.DeepEqual(before, c.snapshot()) || f.stops != 0 || len(f.starts) != 0 || f.unsafe {
		t.Fatal("preparation changed acceptance or runtime")
	}
	if _, err := os.Stat(filepath.Join(c.store.dir, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparation wrote a journal")
	}
}

func TestPreparationRejectsAuthorityBeforePull(t *testing.T) {
	for _, kind := range []string{"signature", "approval", "policy", "replay", "source", "unknown", "legacy", "consumed", "topology", "expired"} {
		t.Run(kind, func(t *testing.T) {
			c, f, a := fixture(t)
			input, _ := c.release()
			switch kind {
			case "signature":
				input.data = []byte(`{"payload":"invalid","signature":"invalid"}`)
			case "approval":
				a.To.API = a.From.API
			case "policy":
				input.policy.Channel = "beta"
			case "replay":
				c.state.HighWater = 2
			case "source":
				c.state.Current = a.To
			case "unknown":
				c.state.CurrentKnown = false
			case "legacy":
				c.state.Version = 1
			case "consumed":
				c.state.Consumed = []string{a.ID}
				c.state.Accepted = []AcceptedRelease{{Approval: a, Sequence: 1, PayloadSHA256: strings.Repeat("a", 64)}}
			case "topology":
				f.install("api", a.To.API)
			case "expired":
				c.now = func() time.Time { return time.Unix(1800003600, 0) }
			}
			e := &preparationExecutor{base: f, approved: a.To}
			c.docker.executor = e
			candidate, err := prepareImages(context.Background(), c.docker, input, a, c.state, c.now)
			if err == nil || candidate != (preparedCandidate{}) || len(e.pulled) != 0 || f.stops != 0 || len(f.starts) != 0 {
				t.Fatalf("invalid authority caused preparation: %v %+v", err, candidate)
			}
		})
	}
}

func TestPreparationRechecksAfterPull(t *testing.T) {
	for _, kind := range []string{"pull failure", "expiry", "runtime drift", "missing target"} {
		t.Run(kind, func(t *testing.T) {
			c, f, a := fixture(t)
			input, _ := c.release()
			e := &preparationExecutor{base: f, approved: a.To}
			now := c.now()
			switch kind {
			case "pull failure":
				e.fail = a.To.Web
			case "expiry":
				e.afterPull = func() { now = time.Unix(1800003600, 0) }
			case "runtime drift":
				e.afterPull = func() { f.install("api", a.To.API) }
			case "missing target":
				e.afterPull = func() { delete(f.images, a.To.API) }
			}
			c.docker.executor = e
			candidate, err := prepareImages(context.Background(), c.docker, input, a, c.state, func() time.Time { return now })
			if err == nil || candidate != (preparedCandidate{}) || f.stops != 0 || len(f.starts) != 0 {
				t.Fatalf("failed preparation reported success: %v", err)
			}
		})
	}
}

func TestPreparationRejectsRecoveryMarkerWithoutChangingJournal(t *testing.T) {
	dir := t.TempDir()
	journal := []byte("do not repair this journal")
	if err := os.WriteFile(filepath.Join(dir, "state.json"), journal, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recovery-required"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreparationState(dir); !errors.Is(err, errBusy) {
		t.Fatalf("recovery not blocked: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || string(got) != string(journal) {
		t.Fatal("preparation repaired journal")
	}
}

func TestPreparationRequiresReconciledOrSuccessfulTerminalState(t *testing.T) {
	for _, phase := range []string{"accepted", "stopping", "applying", "rolling_back", "rejected", "manual_recovery", "succeeded", "rolled_back"} {
		t.Run(phase, func(t *testing.T) {
			c, _, a := fixture(t)
			state := c.state
			hash := strings.Repeat("a", 64)
			state.HighWater = 2
			state.Consumed = []string{a.ID}
			state.Accepted = []AcceptedRelease{{Approval: a, Sequence: 2, PayloadSHA256: hash}}
			state.Operation = &Operation{Approval: a, Phase: phase, Sequence: 2, PayloadSHA256: hash}
			switch phase {
			case "succeeded":
				state.Current = a.To
			case "stopping", "applying", "rolling_back", "manual_recovery":
				state.CurrentKnown = false
			}
			if err := state.validateSigned(); err != nil {
				t.Fatalf("invalid scenario instead of a real phase: %v", err)
			}
			err := preparationState(state)
			want := phase == "succeeded" || phase == "rolled_back"
			if (err == nil) != want {
				t.Fatalf("phase eligibility: %v", err)
			}
			if want {
				state.Operation.Maintenance = true
				if err := preparationState(state); err == nil {
					t.Fatal("maintenance state accepted")
				}
			}
		})
	}
}

func TestPreparationStateDoesNotBypassRootTrust(t *testing.T) {
	c, _, _ := fixture(t)
	if err := c.store.save(c.state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.store.dir, "state.json")
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreparationState(c.store.dir); err == nil {
		t.Fatal("writable or non-root journal accepted")
	}
}
