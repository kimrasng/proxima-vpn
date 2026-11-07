package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func serverUpdatePrepareInput(apply string) string {
	return strings.Replace(apply, `"release_id":"release-one","maintenance":true`, `"tag":"v1.2.3"`, 1)
}

func TestServerUpdatePrepareRejectsInputAndReauthentication(t *testing.T) {
	a, apply := serverUpdateTestIdentity(t)
	valid := serverUpdatePrepareInput(apply)
	var calls atomic.Int32
	t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	for _, tc := range []struct {
		name, body, id, role string
		change               func(*serverUpdateAdmin)
		want                 int
	}{
		{name: "missing identity", body: valid, role: "owner", want: 401},
		{name: "admin", body: valid, id: a.id, role: "admin", want: 403},
		{name: "demoted", body: valid, id: a.id, role: "owner", change: func(a *serverUpdateAdmin) { a.role = "admin" }, want: 403},
		{name: "disabled", body: valid, id: a.id, role: "owner", change: func(a *serverUpdateAdmin) { a.active = false }, want: 403},
		{name: "bad password", body: strings.Replace(valid, "owner-password", "wrong", 1), id: a.id, role: "owner", want: 403},
		{name: "wrong otp", body: valid, id: a.id, role: "owner", change: func(a *serverUpdateAdmin) { a.totpSecret = "GEZDGNBVGY3TQOJQ" }, want: 403},
		{name: "disabled otp", body: valid, id: a.id, role: "owner", change: func(a *serverUpdateAdmin) { a.totpEnabled = false }, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := a
			if tc.change != nil {
				tc.change(&current)
			}
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, current), "POST", "/prepare", tc.body, tc.id, tc.role, nil)
			if status != tc.want || calls.Load() != 0 {
				t.Fatalf("%d %v calls=%d", status, result, calls.Load())
			}
		})
	}
	for name, body := range map[string]string{
		"path":             strings.Replace(valid, "v1.2.3", "../evil", 1),
		"url":              strings.Replace(valid, "v1.2.3", "https://evil.test/v1", 1),
		"long":             strings.Replace(valid, "v1.2.3", "v1"+strings.Repeat("a", 127), 1),
		"empty":            strings.Replace(valid, "v1.2.3", "", 1),
		"wrong prefix":     strings.Replace(valid, "v1.2.3", "main", 1),
		"unicode":          strings.Replace(valid, "v1.2.3", "v1한", 1),
		"extra input":      strings.Replace(valid, `"tag":`, `"url":"https://evil.test","tag":`, 1),
		"apply fields":     apply,
		"missing password": strings.Replace(valid, `"password":"owner-password",`, "", 1),
		"missing otp":      strings.Replace(valid, `"totp_code":`, `"other":`, 1),
		"case alias":       strings.Replace(valid, `"tag":`, `"Tag":`, 1),
		"duplicate":        strings.Replace(valid, `"tag":`, `"tag":"v0","tag":`, 1),
		"null":             strings.Replace(valid, `"v1.2.3"`, "null", 1),
		"trailing":         valid + "{}",
		"large":            valid + strings.Repeat(" ", serverUpdateMaxInput),
	} {
		t.Run(name, func(t *testing.T) {
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), "POST", "/prepare", body, a.id, "owner", nil)
			if status != 400 || result["error"] != "invalid_request" || calls.Load() != 0 {
				t.Fatalf("%d %v calls=%d", status, result, calls.Load())
			}
		})
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/prepare?tag=v1", ""}, {"GET", "/prepare", "{}"}, {"POST", "/prepare?tag=v1", valid},
	} {
		status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), tc.method, tc.path, tc.body, a.id, "owner", nil)
		if status != 400 || calls.Load() != 0 {
			t.Fatalf("%d %v", status, result)
		}
	}
}

func TestServerUpdatePrepareUnixContract(t *testing.T) {
	a, apply := serverUpdateTestIdentity(t)
	valid := serverUpdatePrepareInput(apply)
	for _, tc := range []struct {
		name, method, response string
		host, want             int
	}{
		{"start", "POST", `{"status":"preparing","tag":"v1.2.3"}`, 202, 202},
		{"busy", "POST", `{"error":"blocked"}`, 409, 409},
		{"manual mode", "POST", `{"error":"not_authorized"}`, 403, 403},
		{"invalid", "POST", `{"error":"invalid_request"}`, 400, 400},
		{"private error", "POST", `{"error":"private-secret"}`, 409, 502},
		{"error code mismatch", "POST", `{"error":"not_authorized"}`, 409, 502},
		{"tag mismatch", "POST", `{"status":"preparing","tag":"v2"}`, 202, 502},
		{"wrong accepted state", "POST", `{"status":"idle"}`, 202, 502},
		{"wrong start code", "POST", `{"status":"preparing","tag":"v1.2.3"}`, 200, 502},
		{"idle", "GET", `{"status":"idle"}`, 200, 200},
		{"preparing", "GET", `{"status":"preparing","tag":"v1.2.3"}`, 200, 200},
		{"failed", "GET", `{"status":"failed","tag":"v1.2.3","error":"preparation_failed"}`, 200, 200},
		{"unknown state", "GET", `{"status":"success"}`, 200, 502},
		{"idle extra", "GET", `{"status":"idle","tag":"v1"}`, 200, 502},
		{"private failure", "GET", `{"status":"failed","tag":"v1","error":"private-secret"}`, 200, 502},
		{"duplicate", "GET", `{"status":"idle","status":"idle"}`, 200, 502},
		{"trailing", "GET", `{"status":"idle"}{}`, 200, 502},
		{"bad status code", "GET", `{"status":"idle"}`, 202, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				data, err := io.ReadAll(r.Body)
				wantBody := ""
				if tc.method == "POST" {
					wantBody = `{"tag":"v1.2.3"}`
				}
				if err != nil || string(data) != wantBody || r.Method != tc.method || r.URL.Path != "/v1/prepare" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Idempotency-Key") != "" {
					t.Errorf("unsafe host request: %s %s %s", r.Method, r.URL, data)
				}
				w.WriteHeader(tc.host)
				io.WriteString(w, tc.response)
			}))
			h := serverUpdateTestHandler(t, a)
			var records []services.Record
			h.audit = func(_ context.Context, record services.Record) { records = append(records, record) }
			body := ""
			if tc.method == "POST" {
				body = valid
			}
			status, result := serverUpdateTestRequest(t, h, tc.method, "/prepare", body, a.id, "owner", nil)
			if status != tc.want || calls.Load() != 1 {
				t.Fatalf("%d %v calls=%d", status, result, calls.Load())
			}
			if tc.want < 500 {
				var expected map[string]any
				if err := json.Unmarshal([]byte(tc.response), &expected); err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				want, err := json.Marshal(expected)
				if err != nil || string(encoded) != string(want) {
					t.Fatalf("lost host response: %s want %s", encoded, want)
				}
			} else if result["error"] != "updater_unavailable" {
				t.Fatalf("unsafe failure: %v", result)
			}
			if tc.method == "POST" {
				if len(records) != 2 || records[0].EventType != "server.update_prepare_requested" || records[0].Detail["tag"] != "v1.2.3" || records[1].EventType != "server.update_prepare_response" {
					t.Fatalf("missing audit: %v", records)
				}
				encoded, err := json.Marshal(records)
				if err != nil || strings.Contains(string(encoded), "owner-password") || strings.Contains(string(encoded), "totp") {
					t.Fatalf("unsafe audit: %s", encoded)
				}
			}
		})
	}
}

func TestServerUpdateSignedCandidateAndExactApplyHash(t *testing.T) {
	a, input := serverUpdateTestIdentity(t)
	hash := strings.Repeat("e", 64)
	images := `{"api":"ghcr.io/proxima/api@sha256:` + strings.Repeat("a", 64) + `","web":"ghcr.io/proxima/web@sha256:` + strings.Repeat("b", 64) + `"}`
	candidate := `{"status":"prepared","release_id":"release-one","sequence":2,"from":` + images + `,"to":` + strings.ReplaceAll(images, strings.Repeat("a", 64), strings.Repeat("c", 64)) + `,"rollback_compatible":true,"expires_at":1793797200}`
	prepared := `{"status":"prepared","tag":"v1.2.3","candidate":{"status":"prepared","release_id":"release-one","sequence":2,"payload_sha256":"` + hash + `","to":` + images + `,"expires_at":1793797200}}`
	for _, tc := range []struct {
		name, path, response string
		want                 int
	}{
		{"manual candidate", "/candidate", candidate, 200},
		{"signed candidate", "/candidate", strings.Replace(candidate, `"status":`, `"payload_sha256":"`+hash+`","status":`, 1), 200},
		{"empty candidate hash", "/candidate", strings.Replace(candidate, `"status":`, `"payload_sha256":"","status":`, 1), 502},
		{"prepared", "/prepare", prepared, 200},
		{"invalid hash", "/prepare", strings.Replace(prepared, hash, strings.ToUpper(hash), 1), 502},
		{"missing hash", "/prepare", strings.Replace(prepared, `"payload_sha256":"`+hash+`",`, "", 1), 502},
		{"private metadata", "/prepare", strings.Replace(prepared, `"sequence":2`, `"sequence":2,"policy":{}`, 1), 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.response) }))
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), "GET", tc.path, "", a.id, "owner", nil)
			if status != tc.want {
				t.Fatalf("%d %v", status, result)
			}
			if status == 200 && tc.name == "signed candidate" && result["payload_sha256"] != hash {
				t.Fatalf("lost hash: %v", result)
			}
		})
	}
	for _, tc := range []struct {
		name, value string
		host, want  int
	}{
		{"exact signed", hash, 202, 202}, {"different signed", strings.Repeat("f", 64), 403, 403},
		{"busy signed", hash, 409, 409}, {"uppercase", strings.ToUpper(hash), 0, 400}, {"empty", "", 0, 400}, {"short", "abc", 0, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				want := `{"release_id":"release-one","maintenance":true,"payload_sha256":"` + tc.value + `"}`
				if err != nil || string(body) != want {
					t.Errorf("hash changed or credentials leaked: %s", body)
				}
				w.WriteHeader(tc.host)
				switch tc.host {
				case 202:
					io.WriteString(w, serverUpdateTestState("release-one", "accepted"))
				case 403:
					io.WriteString(w, `{"error":"not_approved"}`)
				case 409:
					io.WriteString(w, `{"error":"blocked"}`)
				}
			}))
			payload := strings.Replace(input, `"release_id":`, `"payload_sha256":"`+tc.value+`","release_id":`, 1)
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), "POST", "/apply", payload, a.id, "owner", nil)
			wantCalls := int32(1)
			if tc.host == 0 {
				wantCalls = 0
			}
			if status != tc.want || calls.Load() != wantCalls {
				t.Fatalf("%d %v calls=%d", status, result, calls.Load())
			}
		})
	}
}

func TestServerUpdatePreparationDoesNotSerializeOrReplayRequests(t *testing.T) {
	a, apply := serverUpdateTestIdentity(t)
	started, finish := make(chan struct{}), make(chan struct{})
	var starts, applies atomic.Int32
	t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/prepare":
			if starts.Add(1) == 1 {
				close(started)
				<-finish
				w.WriteHeader(202)
				io.WriteString(w, `{"status":"preparing","tag":"v1.2.3"}`)
				return
			}
			w.WriteHeader(409)
			io.WriteString(w, `{"error":"blocked"}`)
		case "POST /v1/apply":
			applies.Add(1)
			w.WriteHeader(409)
			io.WriteString(w, `{"error":"blocked"}`)
		case "GET /v1/prepare":
			io.WriteString(w, `{"status":"preparing","tag":"v1.2.3"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	h := serverUpdateTestHandler(t, a)
	h.audit = func(context.Context, services.Record) {}
	done := make(chan int, 1)
	go func() {
		status, _ := serverUpdateTestRequest(t, h, "POST", "/prepare", serverUpdatePrepareInput(apply), a.id, "owner", nil)
		done <- status
	}()
	<-started
	// Complete all concurrent requests before releasing the first response.
	// A bridge mutex or implicit queue would time out these requests.
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/prepare", serverUpdatePrepareInput(apply), 409},
		{"POST", "/apply", apply, 409},
		{"GET", "/prepare", "", 200},
	} {
		status, result := serverUpdateTestRequest(t, h, tc.method, tc.path, tc.body, a.id, "owner", nil)
		if status != tc.want {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, status, result)
		}
	}
	close(finish)
	if status := <-done; status != 202 {
		t.Fatalf("first preparation: %d", status)
	}
	if starts.Load() != 2 || applies.Load() != 1 {
		t.Fatalf("requests replayed: prepare=%d apply=%d", starts.Load(), applies.Load())
	}
}
