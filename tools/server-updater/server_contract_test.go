package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The API contract test runs this test in the standalone updater module and
// consumes actual ServeHTTP bytes, rather than a hand-written host response.
// Only Docker is fake; signature verification, acceptance, journal and recovery
// run normally. No test-only entry point is added to the production daemon.
func TestHTTPContractResponses(t *testing.T) {
	type response struct {
		Name   string          `json:"name"`
		Path   string          `json:"path"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	var responses []response
	capture := func(name, path string, c *Controller) {
		t.Helper()
		method, body := http.MethodGet, ""
		if path == "/v1/apply" {
			method, body = http.MethodPost, `{"release_id":"release-one","maintenance":true}`
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		want := http.StatusOK
		if method == http.MethodPost {
			want = http.StatusAccepted
		}
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		responses = append(responses, response{name, path, w.Code, append(json.RawMessage(nil), w.Body.Bytes()...)})
	}
	c, f, _ := fixture(t)
	capture("signed idle", "/v1/status", c)
	capture("accepted", "/v1/apply", c)
	state := waitOperation(t, c)
	if state.Version != 2 || state.Operation.Phase != "succeeded" || state.HighWater != 2 || len(state.Accepted) != 1 || f.unsafe {
		t.Fatalf("signed operation failed: %+v", state)
	}
	capture("succeeded", "/v1/status", c)
	capture("idempotent succeeded", "/v1/apply", c)
	if len(f.starts) != 2 {
		t.Fatal("idempotent apply restarted containers")
	}

	c, f, _ = fixture(t)
	f.stopErr = errors.New("stop failed")
	capture("recovery accepted", "/v1/apply", c)
	state = waitOperation(t, c)
	if state.CurrentKnown || state.Operation.Error != "recovery_stop_unverified" || f.unsafe {
		t.Fatalf("recovery did not remain unknown: %+v", state)
	}
	capture("recovery", "/v1/status", c)
	capture("idempotent recovery", "/v1/apply", c)

	for _, outcome := range []string{"rejected", "rolled_back", "uncertain recovery"} {
		c, f, a := fixture(t)
		switch outcome {
		case "rejected":
			delete(f.images, a.To.API)
		case "rolled_back":
			c.readiness = readyFunc(func(context.Context, string) error {
				if f.containers["api"].Config.Image == a.To.API {
					return errReadiness
				}
				return nil
			})
		case "uncertain recovery":
			c.docker.executor = &recoveryExecutor{fakeExecutor: f, startUncertain: true,
				stopFailure: "web", stopError: errUncertain}
		}
		if _, err := c.accept(a.ID); err != nil {
			t.Fatal(err)
		}
		waitOperation(t, c)
		if f.unsafe {
			t.Fatal("unexpected Docker command")
		}
		capture(outcome, "/v1/status", c)
	}

	c, _, _ = fixture(t)
	c.state = State{Version: 1}
	capture("legacy idle", "/v1/status", c)

	dir := os.Getenv("PROXIMA_UPDATER_CONTRACT_OUTPUT")
	if dir == "" {
		dir = t.TempDir()
	}
	data, err := json.Marshal(responses)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "responses.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
