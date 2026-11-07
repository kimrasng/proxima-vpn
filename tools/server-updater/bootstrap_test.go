package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapRendersOnlyIdenticalPinnedPairs(t *testing.T) {
	for _, kind := range []string{"identical", "different", "tag", "missing", "invalid_id"} {
		t.Run(kind, func(t *testing.T) {
			c, _, a := fixture(t)
			source := a.From
			a.To = a.From
			switch kind {
			case "different":
				a.To.API = "ghcr.io/proxima/api@sha256:" + strings.Repeat("e", 64)
			case "tag":
				a.From.API, a.To.API = "api:latest", "api:latest"
			case "missing":
				a.From.Web, a.To.Web = "", ""
			case "invalid_id":
				a.ID = "../enroll"
			}
			data, err := a.bootstrap(c.docker.config)
			if kind != "identical" {
				if err == nil || len(data) != 0 {
					t.Fatal("invalid enrollment rendered")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var spec composeSpec
			if err := json.Unmarshal(data, &spec); err != nil {
				t.Fatal(err)
			}
			if spec.Services["api"].Image != source.API || spec.Services["web"].Image != source.Web || len(spec.Services) != 2 {
				t.Fatal("bootstrap changed enrolled images or service scope")
			}
		})
	}
}

func TestBootstrapFilePassesEnrollmentPreflightButCannotApply(t *testing.T) {
	c, f, a := fixture(t)
	a.To = a.From
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "approved.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	c.approval = func() (Approval, error) { return loadApproval(os.DirFS(dir)) }
	loaded, err := c.approval()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.bootstrap(c.docker.config); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/preflight", nil))
	var response preflightResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || !response.OK || response.Reason != "" {
		t.Fatalf("enrollment preflight failed: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(`{"release_id":"`+a.ID+`","maintenance":true}`))
	r.Header.Set("Content-Type", "application/json")
	c.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || c.snapshot().Operation != nil || len(f.starts) != 0 {
		t.Fatal("bootstrap approval authorized an update")
	}
	if len(c.snapshot().Consumed) != 0 || f.unsafe {
		t.Fatal("enrollment mutated operation state or command scope")
	}
	delete(f.containers["api"].Config.Labels, runtimeLabel)
	w = httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/preflight", nil))
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.OK || response.Reason != "topology_mismatch" {
		t.Fatalf("unenrolled deployment passed preflight: %s", w.Body.String())
	}
}
