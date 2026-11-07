package handlers

import (
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

func testServerUpdateCandidateContract(t *testing.T, dir string, a serverUpdateAdmin) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "candidates.json"))
	if err != nil {
		t.Fatal(err)
	}
	var responses []struct {
		Name   string          `json:"name"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(data, &responses); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 18 {
		t.Fatalf("missing actual candidate scenarios: %d", len(responses))
	}
	var prepared string
	for _, response := range responses {
		t.Run("candidate "+response.Name, func(t *testing.T) {
			var calls atomic.Int32
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 || r.Method != "GET" || r.URL.Path != "/v1/candidate" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("candidate request forwarded caller authority")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.Status)
				if _, err := w.Write(response.Body); err != nil {
					t.Error(err)
				}
			}))
			h := serverUpdateTestHandler(t, a)
			h.now = func() time.Time { return time.Unix(1800000100, 0) }
			status, result := serverUpdateTestRequest(t, h, "GET", "/candidate", "", a.id, "owner", nil)
			if status != response.Status || calls.Load() != 1 {
				t.Fatalf("candidate host response lost: %d %v", status, result)
			}
			if status != http.StatusOK {
				if len(result) != 1 || result["error"] != "updater_unavailable" {
					t.Fatalf("unsafe candidate failure: %v", result)
				}
				return
			}
			var expected map[string]any
			if err := json.Unmarshal(response.Body, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result, expected) || len(result) != 7 {
				t.Fatalf("candidate DTO changed: %v; host=%s", result, response.Body)
			}
			if response.Name == "prepared" {
				prepared = string(response.Body)
			}
		})
	}
	if prepared == "" {
		t.Fatal("missing prepared candidate from actual signed verifier")
	}
	// Corrupt real host bytes at the API trust boundary. These cases ensure
	// future host metadata cannot silently broaden the public response.
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"missing host endpoint", `{"error":"not_found"}`, 404},
		{"private host error", `{"error":"private-secret"}`, 503},
		{"redirect", prepared, 307},
		{"wrong success code", prepared, 202},
		{"null", "null", 200},
		{"trailing document", prepared + "{}", 200},
		{"oversized", strings.Repeat("x", serverUpdateMaxOutput+1), 200},
		{"wrong status", strings.Replace(prepared, `"prepared"`, `"approved"`, 1), 200},
		{"bad identity", strings.Replace(prepared, `"release-one"`, `"../release"`, 1), 200},
		{"case alias", strings.Replace(prepared, `"release_id"`, `"Release_ID"`, 1), 200},
		{"duplicate", strings.Replace(prepared, `"sequence":2`, `"sequence":1,"sequence":2`, 1), 200},
		{"zero sequence", strings.Replace(prepared, `"sequence":2`, `"sequence":0`, 1), 200},
		{"unsafe integer", strings.Replace(prepared, `"sequence":2`, `"sequence":9007199254740992`, 1), 200},
		{"fraction", strings.Replace(prepared, `"sequence":2`, `"sequence":2.5`, 1), 200},
		{"null sequence", strings.Replace(prepared, `"sequence":2`, `"sequence":null`, 1), 200},
		{"expired", strings.Replace(prepared, `"expires_at":1800003600`, `"expires_at":1800000100`, 1), 200},
		{"unbounded expiry", strings.Replace(prepared, `"expires_at":1800003600`, `"expires_at":1800604901`, 1), 200},
		{"missing boolean", strings.Replace(prepared, `"rollback_compatible":true,`, "", 1), 200},
		{"null boolean", strings.Replace(prepared, `"rollback_compatible":true`, `"rollback_compatible":null`, 1), 200},
		{"private journal", strings.Replace(prepared, `"status"`, `"policy":{"public_key":"private-secret"},"status"`, 1), 200},
		{"payload digest", strings.Replace(prepared, `"status"`, `"payload_sha256":"private-secret","status"`, 1), 200},
		{"nested unknown", strings.Replace(prepared, `"api":`, `"secret":"private-secret","api":`, 1), 200},
		{"nested duplicate", strings.Replace(prepared, `"api":`, `"api":"private-secret","api":`, 1), 200},
		{"nested alias", strings.Replace(prepared, `"api":`, `"API":`, 1), 200},
		{"null images", strings.Replace(prepared, `"from":`, `"from":null,"extra":`, 1), 200},
		{"mutable image", strings.Replace(prepared, "ghcr.io/proxima/api@sha256:"+strings.Repeat("a", 64), "ghcr.io/proxima/api:latest", 1), 200},
		{"equal pairs", strings.NewReplacer(strings.Repeat("c", 64), strings.Repeat("a", 64), strings.Repeat("d", 64), strings.Repeat("b", 64)).Replace(prepared), 200},
	} {
		t.Run("candidate rejects "+tc.name, func(t *testing.T) {
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Error(err)
				}
			}))
			h := serverUpdateTestHandler(t, a)
			h.now = func() time.Time { return time.Unix(1800000100, 0) }
			status, result := serverUpdateTestRequest(t, h, "GET", "/candidate", "", a.id, "owner", nil)
			want := http.StatusBadGateway
			if tc.status != 200 {
				want = http.StatusServiceUnavailable
			}
			if status != want || len(result) != 1 || result["error"] != "updater_unavailable" {
				t.Fatalf("untrusted candidate returned: %d %v", status, result)
			}
		})
	}
}
