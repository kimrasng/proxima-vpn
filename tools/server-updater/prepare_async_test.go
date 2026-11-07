package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

func publisherFixture(t *testing.T) (*Controller, *preparationExecutor, Approval) {
	t.Helper()
	c, f, a := fixture(t)
	input, err := c.release()
	if err != nil {
		t.Fatal(err)
	}
	c.authorization = func() (authorizationPolicy, error) {
		return authorizationPolicy{Mode: "publisher_signed"}, nil
	}
	c.policy = func() (releaseverify.Policy, error) { return input.policy, nil }
	c.fetch = func(context.Context, string) ([]byte, error) { return input.data, nil }
	c.approval = func() (Approval, error) { return Approval{}, os.ErrNotExist }
	c.release = func() (releaseInput, error) { return releaseInput{}, os.ErrNotExist }
	e := &preparationExecutor{base: f, approved: a.To}
	c.docker.executor = e
	return c, e, a
}

func requestUpdater(c *Controller, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	return w
}

func completePreparation(t *testing.T, c *Controller) preparedCandidate {
	t.Helper()
	w := requestUpdater(c, "POST", "/v1/prepare", `{"tag":"v1.2.3"}`)
	if w.Code != 202 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	waitOperation(t, c)
	w = requestUpdater(c, "GET", "/v1/prepare", "")
	var result preparationResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "prepared" || result.Candidate == nil {
		t.Fatalf("completion: %d %s", w.Code, w.Body.String())
	}
	return *result.Candidate
}

func TestPublisherPreparationAndHashBoundApplyWithoutManualApproval(t *testing.T) {
	// API cross-module tests consume actual handler responses from this signed
	// publisher flow, including preparation without a manual approval file.
	type response struct {
		Name   string          `json:"name"`
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
		// []byte is base64 in JSON, preserving whitespace and the final newline.
		BodyBytes []byte `json:"body_bytes"`
	}
	var responses []response
	c, e, a := publisherFixture(t)
	capture := func(name, method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := requestUpdater(c, method, path, body)
		if w.Code != status || !json.Valid(w.Body.Bytes()) || w.Body.Len() > 16<<10 {
			t.Fatalf("%s: status=%d body bytes=%d", name, w.Code, w.Body.Len())
		}
		bodyBytes := append([]byte(nil), w.Body.Bytes()...)
		responses = append(responses, response{name, method, path, w.Code, json.RawMessage(bodyBytes), bodyBytes})
		return w
	}
	before := c.snapshot()
	delete(e.base.images, a.To.API)
	delete(e.base.images, a.To.Web)
	capture("prepare accepted", "POST", "/v1/prepare", `{"tag":"v1.2.3"}`, 202)
	waitOperation(t, c)
	w := capture("prepare ready", "GET", "/v1/prepare", "", 200)
	var prepared preparationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &prepared); err != nil || prepared.Status != "prepared" || prepared.Candidate == nil {
		t.Fatalf("completion: %s (decode: %v)", w.Body.String(), err)
	}
	candidate := *prepared.Candidate
	if !reflect.DeepEqual(before, c.snapshot()) || e.base.stops != 0 || len(e.base.starts) != 0 {
		t.Fatal("preparation consumed or mutated the deployment")
	}
	data, err := c.store.preparedEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := signedFixtureRelease(t, a, 2)
	if string(data) != string(expected.data) || candidate.ReleaseID != a.ID || candidate.To != a.To {
		t.Fatal("candidate is not the signed fetched transition")
	}
	w = capture("candidate", "GET", "/v1/candidate", "", 200)
	var observed candidateResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &observed) != nil || observed.PayloadSHA256 != candidate.PayloadSHA256 || observed.RollbackCompatible {
		t.Fatalf("candidate lost binding or gained rollback: %s", w.Body.String())
	}
	w = capture("apply accepted", "POST", "/v1/apply", `{"release_id":"`+a.ID+`","maintenance":true,"payload_sha256":"`+candidate.PayloadSHA256+`"}`, 202)
	if w.Code != 202 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	s := waitOperation(t, c)
	if s.Operation.Phase != "succeeded" || s.Current != a.To || s.HighWater != 2 || s.Operation.PayloadSHA256 != candidate.PayloadSHA256 || s.Operation.Approval.RollbackCompatible || e.base.unsafe {
		t.Fatalf("signed apply failed: %+v", s)
	}
	data, err = json.Marshal(responses)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 64<<10 {
		t.Fatalf("preparation contract exceeds 64 KiB: %d", len(data))
	}
	dir := os.Getenv("PROXIMA_UPDATER_CONTRACT_OUTPUT")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.WriteFile(filepath.Join(dir, "preparations.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPreparationDoesNotQueueOrRaceApply(t *testing.T) {
	c, e, a := publisherFixture(t)
	entered := make(chan struct{})
	resume := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.ctx = ctx
	input, _ := signedFixtureRelease(t, a, 2)
	c.fetch = func(ctx context.Context, _ string) ([]byte, error) {
		close(entered)
		select {
		case <-resume:
			return input.data, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	w := requestUpdater(c, "POST", "/v1/prepare", `{"tag":"v1.2.3"}`)
	if w.Code != 202 || strings.TrimSpace(w.Body.String()) != `{"status":"preparing","tag":"v1.2.3"}` {
		t.Fatalf("prepare must return before fetch completes: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch not started")
	}
	if w := requestUpdater(c, "POST", "/v1/prepare", `{"tag":"v2.0.0"}`); w.Code != 409 {
		t.Fatalf("second preparation queued: %d", w.Code)
	}
	if _, err := c.acceptCandidate(a.ID, strings.Repeat("a", 64)); !errors.Is(err, errBusy) {
		t.Fatal("apply overlapped preparation", err)
	}
	if _, err := c.candidate(context.Background()); !errors.Is(err, errBusy) {
		t.Fatal("candidate inspection overlapped preparation", err)
	}
	if got := c.preparationStatus(); got.Status != "preparing" {
		t.Fatal(got)
	}
	close(resume)
	waitOperation(t, c)
	if c.preparationStatus().Status != "prepared" || e.base.stops != 0 || len(e.base.starts) != 0 {
		t.Fatal("preparation mutated containers")
	}
}

func TestApplyRejectsMissingOrChangedPayloadIncludingIdempotence(t *testing.T) {
	for _, kind := range []string{"missing", "wrong", "same id replacement", "idempotent mismatch"} {
		t.Run(kind, func(t *testing.T) {
			c, e, a := publisherFixture(t)
			candidate := completePreparation(t, c)
			hash := candidate.PayloadSHA256
			switch kind {
			case "missing":
				hash = ""
			case "wrong":
				hash = strings.Repeat("a", 64)
			case "same id replacement":
				replacement, _ := signedFixtureRelease(t, a, 3)
				if err := atomicWrite(filepath.Join(c.store.dir, preparedEnvelopeName), replacement.data); err != nil {
					t.Fatal(err)
				}
			case "idempotent mismatch":
				if _, err := c.acceptCandidate(a.ID, hash); err != nil {
					t.Fatal(err)
				}
				waitOperation(t, c)
				if _, err := c.acceptCandidate(a.ID, hash); err != nil {
					t.Fatal("exact replay rejected", err)
				}
				hash = strings.Repeat("a", 64)
			}
			before := c.snapshot()
			starts := len(e.base.starts)
			if _, err := c.acceptCandidate(a.ID, hash); !errors.Is(err, errApproval) {
				t.Fatal("unbound candidate accepted", err)
			}
			if !reflect.DeepEqual(before, c.snapshot()) || len(e.base.starts) != starts {
				t.Fatal("rejected hash mutated journal or containers")
			}
		})
	}
}

func TestPreparedCandidateSurvivesRestartWithFullReverification(t *testing.T) {
	c, e, a := publisherFixture(t)
	candidate := completePreparation(t, c)
	// Job observations are ephemeral; the envelope persists independently.
	c.preparation = preparationResponse{}
	c.authorization = func() (authorizationPolicy, error) { return authorizationPolicy{Mode: "publisher_signed"}, nil }
	r, err := c.candidate(context.Background())
	if err != nil || r.PayloadSHA256 != candidate.PayloadSHA256 {
		t.Fatal("persisted candidate unavailable", err)
	}
	c.now = func() time.Time { return time.Unix(candidate.ExpiresAt, 0) }
	if _, err := c.acceptCandidate(a.ID, candidate.PayloadSHA256); !errors.Is(err, errApproval) || len(e.base.starts) != 0 {
		t.Fatal("restart bypassed expiry", err)
	}
}

func TestPublisherHashMatchesSignedPayload(t *testing.T) {
	c, _, a := publisherFixture(t)
	got := completePreparation(t, c)
	input, _ := signedFixtureRelease(t, a, 2)
	v, err := releaseverify.NewVerifier(input.policy)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := v.Verify(input.data, releaseverify.Context{HighWater: 1, Current: releaseverify.Images(a.From), Now: c.now()})
	if err != nil {
		t.Fatal(err)
	}
	hash := verified.PayloadSHA256()
	if got.PayloadSHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("apply token must be signed payload digest")
	}
}
