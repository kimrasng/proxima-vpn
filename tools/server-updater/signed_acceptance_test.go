package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	rv "github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

func signedFixtureRelease(t *testing.T, a Approval, sequence uint64) (releaseInput, rv.Manifest) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{19}, 32))
	p := rv.Policy{PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), Channel: "stable",
		APIRepository: "ghcr.io/proxima/api", WebRepository: "ghcr.io/proxima/web", MinimumSequence: 1}
	m := rv.Manifest{Schema: 1, Product: "proxima-vpn", Channel: p.Channel, ReleaseID: a.ID, Sequence: sequence,
		IssuedAt: 1800000000, ExpiresAt: 1800003600, From: rv.Images(a.From), To: rv.Images(a.To), RollbackCompatible: a.RollbackCompatible}
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(key, append([]byte(rv.SignatureDomain), payload...))
	data, err := json.Marshal(rv.Envelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(sig)})
	if err != nil {
		t.Fatal(err)
	}
	return releaseInput{data, p}, m
}

func configureSignedFixture(t *testing.T, c *Controller, a Approval) {
	t.Helper()
	input, _ := signedFixtureRelease(t, a, 2)
	c.state = State{Version: 2, Product: "proxima-vpn", Policy: input.policy, Current: a.From, CurrentKnown: true,
		HighWater: 1, Consumed: []string{}, Accepted: []AcceptedRelease{}}
	c.release = func() (releaseInput, error) { return input, nil }
	c.now = func() time.Time { return time.Unix(1800000100, 0) }
}

func TestSignedApplyRejectsInvalidAuthorityWithoutMutation(t *testing.T) {
	for _, kind := range []string{"missing", "bad_signature", "expired", "wrong_source", "altered_approval", "policy_rotation", "legacy", "unknown_current", "zero_sequence", "missing_loader"} {
		t.Run(kind, func(t *testing.T) {
			c, f, a := fixture(t)
			input, _ := c.release()
			switch kind {
			case "missing":
				c.release = func() (releaseInput, error) { return releaseInput{}, os.ErrNotExist }
			case "bad_signature":
				var envelope rv.Envelope
				if err := json.Unmarshal(input.data, &envelope); err != nil {
					t.Fatal(err)
				}
				envelope.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64))
				var err error
				input.data, err = json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
				c.release = func() (releaseInput, error) { return input, nil }
			case "expired":
				c.now = func() time.Time { return time.Unix(1800003600, 0) }
			case "wrong_source":
				c.state.Current = a.To
			case "altered_approval":
				a.To.API = a.From.API
				c.approval = func() (Approval, error) { return a, nil }
			case "policy_rotation":
				input.policy.Channel = "beta"
				c.release = func() (releaseInput, error) { return input, nil }
			case "legacy":
				c.state = State{Version: 1}
			case "unknown_current":
				c.state.CurrentKnown = false
			case "zero_sequence":
				c.state.HighWater = 0
			case "missing_loader":
				c.release = nil
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(`{"release_id":"`+a.ID+`","maintenance":true}`))
			r.Header.Set("Content-Type", "application/json")
			c.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || f.stops != 0 || len(f.starts) != 0 || c.state.Operation != nil || len(c.state.Consumed) != 0 {
				t.Fatalf("invalid authority accepted: %d %+v", w.Code, c.state)
			}
			if _, err := os.Stat(filepath.Join(c.store.dir, "state.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejection wrote journal: %v", err)
			}
		})
	}
}

func TestSignedAcceptanceIsDurableBeforeDockerAndConcurrentReplay(t *testing.T) {
	c, f, a := fixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	c.readiness = readyFunc(func(ctx context.Context, service string) error {
		if service == "api" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	_, m := signedFixtureRelease(t, a, 2)
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	c.docker.executor = acceptanceWitness{t, c.store, f, hex.EncodeToString(digest[:])}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := c.accept(a.ID); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("operation not started")
	}
	// Replacement after acceptance must never change the stored payload or approval.
	c.mu.Lock()
	c.release = func() (releaseInput, error) { return releaseInput{}, os.ErrNotExist }
	c.approval = func() (Approval, error) { return Approval{}, errInvalid }
	c.now = func() time.Time { return time.Unix(m.ExpiresAt+1, 0) }
	c.mu.Unlock()
	close(release)
	s := waitOperation(t, c)
	if len(f.starts) != 2 || f.stops != 1 || len(s.Accepted) != 1 || s.HighWater != 2 || s.Current != a.To || !s.CurrentKnown || s.Operation.PayloadSHA256 != hex.EncodeToString(digest[:]) || s.Operation.Approval != a {
		t.Fatalf("acceptance lost: %+v", s)
	}
	persisted, err := c.store.load()
	if err != nil {
		t.Fatal(err)
	}
	c.state = persisted
	if _, err := c.accept(a.ID); err != nil || len(f.starts) != 2 {
		t.Fatal("idempotent restart reverified or executed", err)
	}
}

// Inspect the real atomic journal before the first Docker command, without
// Store.load's deliberate crash-recovery conversion of in-progress operations.
type acceptanceWitness struct {
	t      *testing.T
	store  Store
	next   Executor
	digest string
}

func (w acceptanceWitness) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(w.store.dir, "state.json"))
	if err != nil {
		w.t.Error(err)
		return nil, err
	}
	var state State
	if err := strictJSON(data, &state); err != nil {
		w.t.Error(err)
		return nil, err
	}
	if state.HighWater != 2 || len(state.Accepted) != 1 || state.Operation == nil || state.Operation.PayloadSHA256 != w.digest || state.Accepted[0].PayloadSHA256 != w.digest {
		w.t.Error("Docker ran before atomic acceptance")
		return nil, errInvalid
	}
	return w.next.Run(ctx, binary, args...)
}
