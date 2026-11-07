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
	"time"
)

func TestPreflightEndpointReportsEnrollmentAndApprovalState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Controller, *fakeExecutor, Approval)
		reason string
		ok     bool
	}{
		{"enrolled and approved", func(*Controller, *fakeExecutor, Approval) {}, "", true},
		{"unmanaged deployment", func(_ *Controller, f *fakeExecutor, _ Approval) {
			info := f.containers["api"]
			delete(info.Config.Labels, runtimeLabel)
			f.containers["api"] = info
		}, "topology_mismatch", false},
		{"no approval on record", func(c *Controller, _ *fakeExecutor, _ Approval) {
			c.approval = func() (Approval, error) { return Approval{}, errInvalid }
		}, "not_approved", false},
		{"operation running", func(c *Controller, _ *fakeExecutor, _ Approval) {
			c.active = true
		}, "busy", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, a := fixture(t)
			tc.mutate(c, f, a)
			w := httptest.NewRecorder()
			c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/preflight", nil))
			var resp preflightResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.OK != tc.ok || resp.Reason != tc.reason {
				t.Fatalf("%s: code=%d body=%s", tc.name, w.Code, w.Body.String())
			}
			if len(f.starts) != 0 {
				t.Fatal("preflight mutated deployment")
			}
		})
	}
}

func TestConcurrentApplyAndReplayAreIdempotent(t *testing.T) {
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
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	_, replayErr := c.accept(a.ID)
	_, busyErr := c.accept("another-release")
	close(release)
	waitOperation(t, c)
	if replayErr != nil || !errors.Is(busyErr, errBusy) || len(f.starts) != 2 {
		t.Fatalf("replay=%v busy=%v starts=%d", replayErr, busyErr, len(f.starts))
	}
	state, err := c.store.load()
	if err != nil {
		t.Fatal(err)
	}
	c.state = state
	if _, err := c.accept(a.ID); err != nil || len(f.starts) != 2 {
		t.Fatal("persisted replay executed again")
	}
}

func TestInterruptedStateRequiresManualRecovery(t *testing.T) {
	for _, phase := range []string{"accepted", "stopping", "applying", "rolling_back"} {
		t.Run(phase, func(t *testing.T) {
			c, f, a := fixture(t)
			if err := c.store.save(State{Version: 1, Consumed: []string{a.ID}, Operation: &Operation{Approval: a, Phase: phase}}); err != nil {
				t.Fatal(err)
			}
			s, err := c.store.load()
			if err != nil {
				t.Fatal(err)
			}
			c.state = s
			_, err = c.accept("new-release")
			if !errors.Is(err, errBusy) || !s.Operation.Maintenance || !s.Operation.MigrationUncertain || len(f.starts) != 0 {
				t.Fatalf("unsafe restart: %+v %v", s, err)
			}
		})
	}
}

func TestStateWriteFailurePreventsDockerMutation(t *testing.T) {
	c, f, a := fixture(t)
	c.store.dir = filepath.Join(t.TempDir(), "absent")
	if _, err := c.accept(a.ID); err == nil {
		t.Fatal("accepted without durable state")
	}
	if len(f.starts) != 0 || c.active {
		t.Fatal("mutation without journal")
	}
}

func TestExclusiveLock(t *testing.T) {
	dir := t.TempDir()
	first, err := lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := lock(dir)
	if err == nil {
		second.Close()
		t.Fatal("second controller acquired lock")
	}
}

func TestProtocolRejectsPrivilegeExpansion(t *testing.T) {
	c, f, _ := fixture(t)
	for _, body := range []string{
		`{"release_id":"release-one","maintenance":false}`,
		`{"release_id":"../../etc/passwd","maintenance":true}`,
		`{"release_id":"release-one","maintenance":true,"command":"id"}`,
		`{"release_id":"release-one","maintenance":true,"images":{"api":"evil"}}`,
		`{"release_id":"release-one","maintenance":true,"path":"/tmp/x"}`,
		`{"release_id":"https://github.com/proxima/releases/tag/v2","maintenance":true}`,
		`{"release_id":"release-one","maintenance":true,"maintenance":false}`,
		`{"release_id":"release-one","maintenance":true} {}`,
		strings.Repeat("x", 1025),
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		c.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", body, w.Code)
		}
	}
	if len(f.starts) != 0 || c.state.Operation != nil {
		t.Fatal("invalid request created operation")
	}
}

func TestUnapprovedReleaseAndStatusSecretIsolation(t *testing.T) {
	c, _, _ := fixture(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(`{"release_id":"unknown","maintenance":true}`))
	r.Header.Set("Content-Type", "application/json")
	c.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	var state State
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Body.String())
	}
}

func TestStopFailureDoesNotStartOrRollback(t *testing.T) {
	c, f, a := fixture(t)
	f.stopErr = errors.New("stop failed")
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s := waitOperation(t, c)
	if s.Operation.Phase != "manual_recovery" || len(f.starts) != 0 || s.Operation.MigrationUncertain {
		t.Fatalf("unsafe stop failure: %+v", s.Operation)
	}
}

func TestComposeEscapesInterpolationAndKeepsExistingVolumes(t *testing.T) {
	c, _, a := fixture(t)
	b, err := c.docker.config.compose(a.To)
	if err != nil {
		t.Fatal(err)
	}
	var spec composeSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Services["api"].Environment["JWT_SECRET"] != "secret$$literal" || spec.Volumes["app"].Name != "proxima_app_data" || !spec.Volumes["app"].External || spec.Services["api"].Image != a.To.API {
		t.Fatal("runtime contract changed")
	}
}

func TestCorruptStateFailsClosed(t *testing.T) {
	c, _, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(c.store.dir, "state.json"), []byte(`{"version":1,"operation":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.load(); err == nil {
		t.Fatal("corrupt state accepted")
	}
}
