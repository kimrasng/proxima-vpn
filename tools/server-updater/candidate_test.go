package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This is also consumed over a real Unix socket by the API contract test.
// Only Docker and protected file loading are fixtures; verification is real.
func TestHTTPCandidateContractResponses(t *testing.T) {
	type response struct {
		Name   string          `json:"name"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	var responses []response
	for _, kind := range []string{"prepared", "root disables rollback", "one image changes", "missing", "expired", "bad signature", "approval mismatch", "policy mismatch", "source mismatch", "missing image", "topology mismatch", "busy", "blocked", "legacy", "recovery marker", "consumed", "expires during inspection", "replaced during inspection"} {
		t.Run(kind, func(t *testing.T) {
			c, f, a := fixture(t)
			input, err := c.release()
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusServiceUnavailable
			switch kind {
			case "prepared":
				want = http.StatusOK
			case "root disables rollback":
				a.RollbackCompatible = false
				c.approval = func() (Approval, error) { return a, nil }
				want = http.StatusOK
			case "one image changes":
				a.To.Web = a.From.Web
				input, _ = signedFixtureRelease(t, a, 2)
				c.release = func() (releaseInput, error) { return input, nil }
				c.approval = func() (Approval, error) { return a, nil }
				want = http.StatusOK
			case "missing":
				c.release = func() (releaseInput, error) { return releaseInput{}, os.ErrNotExist }
			case "expired":
				c.now = func() time.Time { return time.Unix(1800003600, 0) }
			case "bad signature":
				input.data = bytes.Replace(input.data, []byte(`"signature":"`), []byte(`"signature":"AAAA`), 1)
				c.release = func() (releaseInput, error) { return input, nil }
			case "approval mismatch":
				a.ID = "other-release"
				c.approval = func() (Approval, error) { return a, nil }
			case "policy mismatch":
				input.policy.Channel = "beta"
				c.release = func() (releaseInput, error) { return input, nil }
			case "source mismatch":
				c.state.Current = a.To
			case "missing image":
				delete(f.images, a.To.API)
			case "topology mismatch":
				f.install("api", a.To.API)
			case "busy":
				c.active = true
			case "blocked":
				c.blocked = true
			case "legacy":
				c.state = State{Version: 1}
			case "recovery marker":
				if err := c.store.requireRecovery(); err != nil {
					t.Fatal(err)
				}
			case "consumed":
				if _, err := c.accept(a.ID); err != nil {
					t.Fatal(err)
				}
				waitOperation(t, c)
			case "expires during inspection":
				c.docker.executor = candidateInspection{f, func() { c.now = func() time.Time { return time.Unix(1800003600, 0) } }}
			case "replaced during inspection":
				replacement, _ := signedFixtureRelease(t, a, 3)
				c.docker.executor = candidateInspection{f, func() { c.release = func() (releaseInput, error) { return replacement, nil } }}
			}
			before := c.snapshot()
			starts, stops := len(f.starts), f.stops
			entries, err := os.ReadDir(c.store.dir)
			if err != nil {
				t.Fatal(err)
			}
			files := make(map[string][]byte)
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(c.store.dir, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				files[entry.Name()] = data
			}
			w := httptest.NewRecorder()
			c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/candidate", nil))
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("candidate: %d %s", w.Code, w.Body.String())
			}
			if want == http.StatusOK {
				var got candidateResponse
				if err := strictJSON(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				expected := candidateResponse{Status: "prepared", ReleaseID: a.ID, Sequence: 2, From: imagesResponse(a.From), To: imagesResponse(a.To), RollbackCompatible: a.RollbackCompatible, ExpiresAt: 1800003600}
				if got != expected {
					t.Fatalf("candidate differs from signed root approval: %+v", got)
				}
			} else if strings.TrimSpace(w.Body.String()) != `{"error":"candidate_unavailable"}` {
				t.Fatalf("failure disclosed details: %s", w.Body.String())
			}
			if !reflect.DeepEqual(before, c.snapshot()) || len(f.starts) != starts || f.stops != stops || f.unsafe {
				t.Fatal("candidate mutated controller or Docker")
			}
			after, err := os.ReadDir(c.store.dir)
			if err != nil || len(after) != len(files) {
				t.Fatalf("candidate changed persisted files: %v", err)
			}
			for _, entry := range after {
				data, err := os.ReadFile(filepath.Join(c.store.dir, entry.Name()))
				if err != nil || !bytes.Equal(data, files[entry.Name()]) {
					t.Fatalf("candidate changed %s: %v", entry.Name(), err)
				}
			}
			responses = append(responses, response{kind, w.Code, append(json.RawMessage(nil), w.Body.Bytes()...)})
		})
	}
	data, err := json.Marshal(responses)
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("PROXIMA_UPDATER_CONTRACT_OUTPUT")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.WriteFile(filepath.Join(dir, "candidates.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

type candidateInspection struct {
	next  Executor
	after func()
}

func (e candidateInspection) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	result, err := e.next.Run(ctx, binary, args...)
	e.after()
	return result, err
}

func TestCandidateRejectsRequestAuthorityAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		cancel, locked     bool
		want               int
	}{
		{"GET", "/v1/candidate?release_id=other", "", false, false, 400},
		{"GET", "/v1/candidate", "{}", false, false, 400},
		{"POST", "/v1/candidate", "", false, false, 404},
		{"GET", "/v1/candidate", "", true, false, 503},
		{"GET", "/v1/candidate", "", false, true, 503},
	} {
		c, f, _ := fixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		if tc.cancel {
			cancel()
		}
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)).WithContext(ctx)
		w := httptest.NewRecorder()
		if tc.locked {
			c.mu.Lock()
		}
		c.ServeHTTP(w, r)
		if tc.locked {
			c.mu.Unlock()
		}
		cancel()
		if w.Code != tc.want || f.unsafe || len(f.starts) != 0 || f.stops != 0 {
			t.Fatalf("unexpected request outcome: %d %s", w.Code, w.Body.String())
		}
	}
}
