package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testServerUpdatePreparationContract(t *testing.T, dir string, a serverUpdateAdmin, authenticatedApply string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "preparations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var responses []struct {
		Name      string          `json:"name"`
		Method    string          `json:"method"`
		Path      string          `json:"path"`
		Status    int             `json:"status"`
		Body      json.RawMessage `json:"body"`
		BodyBytes []byte          `json:"body_bytes"`
	}
	if err := json.Unmarshal(data, &responses); err != nil {
		t.Fatal(err)
	}
	scenarios := []struct {
		name, method, path string
		status             int
	}{
		{"prepare accepted", "POST", "/v1/prepare", 202},
		{"prepare ready", "GET", "/v1/prepare", 200},
		{"candidate", "GET", "/v1/candidate", 200},
		{"apply accepted", "POST", "/v1/apply", 202},
	}
	if len(responses) != len(scenarios) {
		t.Fatalf("missing publisher host scenarios: got %d, want %d", len(responses), len(scenarios))
	}
	var prepared map[string]any
	for i, response := range responses {
		want := scenarios[i]
		if response.Name != want.name || response.Method != want.method || response.Path != want.path || response.Status != want.status {
			t.Fatalf("unexpected publisher scenario %d: %+v", i, response)
		}
		if !t.Run("publisher "+response.Name, func(t *testing.T) {
			var compact bytes.Buffer
			if err := json.Compact(&compact, response.BodyBytes); err != nil || !bytes.Equal(compact.Bytes(), response.Body) {
				t.Fatalf("missing or inconsistent exact host bytes: %v", err)
			}
			apiBody, hostBody := "", ""
			switch response.Name {
			case "prepare accepted":
				apiBody = serverUpdatePrepareInput(authenticatedApply)
				hostBody = `{"tag":"v1.2.3"}`
			case "apply accepted":
				hash, ok := prepared["payload_sha256"].(string)
				if !ok || !serverUpdateHash.MatchString(hash) || prepared["release_id"] != "release-one" {
					t.Fatalf("missing signed candidate binding: %v", prepared)
				}
				apiBody = strings.Replace(authenticatedApply, `"release_id":`, `"payload_sha256":"`+hash+`","release_id":`, 1)
				hostBody = `{"release_id":"release-one","maintenance":true,"payload_sha256":"` + hash + `"}`
			}
			var calls atomic.Int32
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != hostBody || r.Method != response.Method || r.URL.Path != response.Path || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Idempotency-Key") != "" {
					t.Errorf("publisher request changed or leaked authority: %s %s %q (%v)", r.Method, r.URL, body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.Status)
				if _, err := w.Write(response.BodyBytes); err != nil {
					t.Error(err)
				}
			}))
			h := serverUpdateTestHandler(t, a)
			if response.Method == "GET" {
				// Match the signed host fixture's clock for expiry validation.
				// POST retains the identity fixture clock for real TOTP verification.
				h.now = func() time.Time { return time.Unix(1800000100, 0) }
			}
			status, result := serverUpdateTestRequest(t, h, response.Method, strings.TrimPrefix(response.Path, "/v1"), apiBody, a.id, "owner", nil)
			var expected map[string]any
			if err := json.Unmarshal(response.BodyBytes, &expected); err != nil {
				t.Fatal(err)
			}
			if status != response.Status || calls.Load() != 1 || !reflect.DeepEqual(result, expected) {
				t.Fatalf("actual publisher response changed: status=%d calls=%d result=%v host=%s", status, calls.Load(), result, response.BodyBytes)
			}
			switch response.Name {
			case "prepare ready":
				var ok bool
				prepared, ok = result["candidate"].(map[string]any)
				if !ok {
					t.Fatalf("prepared candidate missing: %v", result)
				}
			case "candidate":
				for _, field := range []string{"status", "release_id", "sequence", "payload_sha256", "to", "expires_at"} {
					if !reflect.DeepEqual(result[field], prepared[field]) {
						t.Fatalf("prepared candidate %s changed: %v vs %v", field, result, prepared)
					}
				}
			}
		}) {
			t.FailNow()
		}
	}
}
