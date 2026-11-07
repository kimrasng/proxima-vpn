package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreparationEndpointDefaultsToManualAuthorization(t *testing.T) {
	c, f, _ := fixture(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/prepare", strings.NewReader(`{"tag":"v1.2.3"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || strings.TrimSpace(w.Body.String()) != `{"error":"not_authorized"}` {
		t.Fatalf("manual policy must explicitly deny remote preparation: %d %s", w.Code, w.Body.String())
	}
	// Fully provisioned publisher host whose root keeps manual mode must still
	// be denied; this distinguishes the mode gate from a missing-configuration
	// failure. Revoking after enrollment must not enable remote preparation.
	c, e, _ := publisherFixture(t)
	c.authorization = func() (authorizationPolicy, error) { return authorizationPolicy{Mode: "manual"}, nil }
	w = requestUpdater(c, "POST", "/v1/prepare", `{"tag":"v1.2.3"}`)
	if w.Code != http.StatusForbidden || strings.TrimSpace(w.Body.String()) != `{"error":"not_authorized"}` {
		t.Fatalf("revoked delegation still prepares: %d %s", w.Code, w.Body.String())
	}
	if c.active || c.preparing || len(e.pulled) != 0 {
		t.Fatal("denied preparation started a worker")
	}
	if c.snapshot().Operation != nil || f.stops != 0 || len(f.starts) != 0 || f.unsafe {
		t.Fatal("denied preparation changed runtime")
	}
}

func TestPreparationStatusIsSeparateFromApplyJournal(t *testing.T) {
	c, _, _ := fixture(t)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/prepare", nil))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"idle"}` {
		t.Fatalf("missing preparation observation: %d %s", w.Code, w.Body.String())
	}
}

func TestPrepareRejectsRequestAuthority(t *testing.T) {
	for _, body := range []string{
		`{"tag":"https://evil.example/release"}`, `{"tag":"../v1"}`,
		`{"tag":"v1/../v2"}`, `{"tag":"v1?token=x"}`,
		`{"tag":"v1","url":"https://evil.example"}`,
		`{"tag":"v1","path":"/tmp/release.json"}`,
		`{"tag":"v1","images":{"api":"evil"}}`,
		`{"tag":"v1","public_key":"evil"}`,
		`{"tag":"v1","tag":"v2"}`, `{"Tag":"v1"}`,
		`{"tag":null}`, `{"tag":"v1"}{}`, strings.Repeat("x", 1025),
	} {
		c, e, _ := publisherFixture(t)
		w := requestUpdater(c, "POST", "/v1/prepare", body)
		if w.Code != 400 || c.active || len(e.pulled) != 0 {
			t.Fatalf("invalid prepare request: %d %s", w.Code, body)
		}
	}
}
