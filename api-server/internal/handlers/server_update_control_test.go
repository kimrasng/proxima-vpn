package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"golang.org/x/crypto/bcrypt"
)

var serverUpdateTestTime = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func serverUpdateTestIdentity(t *testing.T) (serverUpdateAdmin, string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("owner-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	a := serverUpdateAdmin{id: "owner-id", email: "owner@example.test", role: "owner", active: true,
		passwordHash: string(hash), totpEnabled: true, totpSecret: "JBSWY3DPEHPK3PXP"}
	code, err := totp.GenerateCode(a.totpSecret, serverUpdateTestTime)
	if err != nil {
		t.Fatal(err)
	}
	return a, `{"release_id":"release-one","maintenance":true,"password":"owner-password","totp_code":"` + code + `"}`
}

func serverUpdateTestSocket(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	// macOS Unix socket paths cannot accommodate long t.TempDir test names.
	dir, err := os.MkdirTemp("/tmp", "up-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	})
	return path
}

func serverUpdateTestHandler(t *testing.T, a serverUpdateAdmin) *ServerUpdateControlHandler {
	t.Helper()
	h := NewServerUpdateControlHandler(nil, nil)
	h.loadAdmin = func(ctx context.Context, id string) (serverUpdateAdmin, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > serverUpdateTimeout {
			t.Error("unbounded identity lookup")
		}
		if id != "owner-id" {
			return serverUpdateAdmin{}, pgx.ErrNoRows
		}
		return a, nil
	}
	h.now = func() time.Time { return serverUpdateTestTime }
	return h
}

func serverUpdateTestRequest(t *testing.T, h *ServerUpdateControlHandler, method, target, payload, id, role string, ctx context.Context) (int, map[string]any) {
	t.Helper()
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("admin_id", id)
		c.Locals("admin_role", role)
		if ctx != nil {
			c.SetUserContext(ctx)
		}
		return c.Next()
	})
	app.All("/status", h.Status)
	app.All("/preflight", h.Preflight)
	app.All("/candidate", h.Candidate)
	app.Get("/prepare", h.PreparationStatus)
	app.Post("/prepare", h.Prepare)
	app.All("/apply", h.Apply)
	req := httptest.NewRequest(method, target, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer private-session-token")
	req.Header.Set("Cookie", "private-cookie")
	resp, err := app.Test(req, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("response is cacheable")
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func serverUpdateTestState(id, phase string) string {
	from := `{"api":"ghcr.io/proxima/api@sha256:` + strings.Repeat("a", 64) + `","web":"ghcr.io/proxima/web@sha256:` + strings.Repeat("b", 64) + `"}`
	to := strings.ReplaceAll(from, strings.Repeat("a", 64), strings.Repeat("c", 64))
	return `{"version":1,"consumed":["` + id + `"],"operation":{"approval":{"id":"` + id + `","from":` + from + `,"to":` + to + `,"rollback_compatible":true},"phase":"` + phase + `","maintenance":true,"migration_uncertain":true,"error":"apply_failed"}}`
}

func TestServerUpdateControlUnixProtocol(t *testing.T) {
	a, payload := serverUpdateTestIdentity(t)
	var calls atomic.Int32
	socket := serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("caller credentials/query/replay header reached updater")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/status":
			_, _ = io.WriteString(w, `{"version":1,"consumed":[],"operation":null}`)
		case "GET /v1/preflight":
			_, _ = io.WriteString(w, `{"ok":false,"reason":"not_approved"}`)
		case "POST /v1/apply":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if string(data) != `{"release_id":"release-one","maintenance":true}` || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("unsafe host payload: %q", data)
			}
			w.WriteHeader(202)
			_, _ = io.WriteString(w, serverUpdateTestState("release-one", "manual_recovery"))
		default:
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	t.Setenv("PROXIMA_UPDATER_SOCKET", socket)
	h := serverUpdateTestHandler(t, a)
	// Environment changes after construction cannot redirect an owner request.
	t.Setenv("PROXIMA_UPDATER_SOCKET", "/tmp/never-contact-this.sock")
	var audit []services.Record
	h.audit = func(_ context.Context, r services.Record) { audit = append(audit, r) }
	status, body := serverUpdateTestRequest(t, h, "GET", "/status", "", a.id, "owner", nil)
	if status != 200 || body["version"] != float64(1) || body["operation"] != nil {
		t.Fatalf("status: %d %v", status, body)
	}
	status, body = serverUpdateTestRequest(t, h, "GET", "/preflight", "", a.id, "owner", nil)
	if status != 200 || body["ok"] != false || body["reason"] != "not_approved" {
		t.Fatalf("preflight: %d %v", status, body)
	}
	status, body = serverUpdateTestRequest(t, h, "POST", "/apply", payload, a.id, "owner", nil)
	op, ok := body["operation"].(map[string]any)
	if status != 202 || !ok || op["phase"] != "manual_recovery" || op["maintenance"] != true || op["migration_uncertain"] != true || op["error"] != "apply_failed" {
		t.Fatalf("false success or lost recovery state: %d %v", status, body)
	}
	if calls.Load() != 3 || len(audit) != 2 || audit[0].EventType != "server.update_requested" || audit[1].EventType != "server.update_response" {
		t.Fatalf("calls=%d audit=%v", calls.Load(), audit)
	}
	for _, record := range audit {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if record.ActorID != a.id || strings.Contains(string(encoded), "owner-password") || strings.Contains(string(encoded), a.totpSecret) || len(record.Detail) != 2 {
			t.Errorf("unsafe audit: %s", encoded)
		}
	}
}

func TestServerUpdateControlFreshOwnerRequired(t *testing.T) {
	a, payload := serverUpdateTestIdentity(t)
	var calls atomic.Int32
	t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	for _, endpoint := range []string{"/status", "/preflight", "/candidate", "/apply", "/prepare"} {
		for _, tc := range []struct {
			name, id, role string
			change         func(*serverUpdateAdmin)
			err            error
			status         int
		}{
			{name: "no identity", role: "owner", status: 401},
			{name: "regular admin", id: a.id, role: "admin", status: 403},
			{name: "demoted", id: a.id, role: "owner", change: func(a *serverUpdateAdmin) { a.role = "admin" }, status: 403},
			{name: "disabled", id: a.id, role: "owner", change: func(a *serverUpdateAdmin) { a.active = false }, status: 403},
			{name: "different identity", id: a.id, role: "owner", change: func(a *serverUpdateAdmin) { a.id = "other-owner" }, status: 403},
			{name: "deleted", id: a.id, role: "owner", err: pgx.ErrNoRows, status: 401},
			{name: "database unavailable", id: a.id, role: "owner", err: errors.New("private database DSN"), status: 503},
		} {
			t.Run(endpoint+tc.name, func(t *testing.T) {
				current := a
				if tc.change != nil {
					tc.change(&current)
				}
				h := serverUpdateTestHandler(t, current)
				if tc.err != nil {
					h.loadAdmin = func(context.Context, string) (serverUpdateAdmin, error) { return current, tc.err }
				}
				method, body := "GET", ""
				if endpoint == "/apply" {
					method, body = "POST", payload
				}
				status, result := serverUpdateTestRequest(t, h, method, endpoint, body, tc.id, tc.role, nil)
				if status != tc.status || calls.Load() != 0 || strings.Contains(result["error"].(string), "private") {
					t.Fatalf("%d %v calls=%d", status, result, calls.Load())
				}
			})
		}
	}
}

func TestServerUpdateControlApplyRejectsUnsafeInput(t *testing.T) {
	a, valid := serverUpdateTestIdentity(t)
	var calls atomic.Int32
	t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	for name, body := range map[string]string{
		"missing consent":   strings.Replace(valid, `"maintenance":true,`, "", 1),
		"false consent":     strings.Replace(valid, `"maintenance":true`, `"maintenance":false`, 1),
		"null consent":      strings.Replace(valid, `"maintenance":true`, `"maintenance":null`, 1),
		"duplicate consent": strings.Replace(valid, `"maintenance":true`, `"maintenance":false,"maintenance":true`, 1),
		"case alias":        strings.Replace(valid, "maintenance", "Maintenance", 1),
		"unknown path":      strings.Replace(valid, `"release_id"`, `"path":"/tmp/evil","release_id"`, 1),
		"unknown image":     strings.Replace(valid, `"release_id"`, `"image":"evil:latest","release_id"`, 1),
		"arbitrary command": strings.Replace(valid, `"release_id"`, `"command":"reboot","release_id"`, 1),
		"identity override": strings.Replace(valid, `"release_id"`, `"admin_id":"other-owner","release_id"`, 1),
		"bad release":       strings.Replace(valid, "release-one", "../../control", 1),
		"long release":      strings.Replace(valid, "release-one", strings.Repeat("a", 65), 1),
		"trailing object":   valid + "{}", "null": "null", "array": "[]", "malformed": "{",
		"too large":         valid + strings.Repeat(" ", serverUpdateMaxInput),
		"too long password": strings.Replace(valid, "owner-password", strings.Repeat("p", 73), 1),
		"empty password":    strings.Replace(valid, "owner-password", "", 1),
		"invalid UTF8":      strings.Replace(valid, "owner-password", string([]byte{0xff}), 1),
	} {
		t.Run(name, func(t *testing.T) {
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), "POST", "/apply", body, a.id, "owner", nil)
			if status != 400 || result["error"] != "invalid_request" || calls.Load() != 0 {
				t.Fatalf("%d %v calls=%d", status, result, calls.Load())
			}
		})
	}
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/status", "", 405}, {"GET", "/apply", "", 405},
		{"GET", "/status?socket=/tmp/evil", "", 400}, {"GET", "/preflight", "{}", 400}, {"POST", "/apply?release_id=evil", valid, 400},
		{"POST", "/candidate", "", 405}, {"GET", "/candidate?release_id=evil", "", 400}, {"GET", "/candidate", "{}", 400},
	} {
		status, _ := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), tc.method, tc.path, tc.body, a.id, "owner", nil)
		if status != tc.want || calls.Load() != 0 {
			t.Fatalf("%s %s status=%d", tc.method, tc.path, status)
		}
	}
}

func TestServerUpdateControlRequiresPasswordAndEnabledTOTP(t *testing.T) {
	a, valid := serverUpdateTestIdentity(t)
	var calls atomic.Int32
	t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	for _, tc := range []struct {
		name   string
		change func(*serverUpdateAdmin)
		body   string
	}{
		{"wrong password", nil, strings.Replace(valid, "owner-password", "wrong-password", 1)},
		{"disabled TOTP", func(a *serverUpdateAdmin) { a.totpEnabled = false }, valid},
		{"empty secret", func(a *serverUpdateAdmin) { a.totpSecret = "" }, valid},
		{"different current secret", func(a *serverUpdateAdmin) { a.totpSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" }, valid},
		{"changed password", func(a *serverUpdateAdmin) { a.passwordHash = "invalid hash" }, valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := a
			if tc.change != nil {
				tc.change(&current)
			}
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, current), "POST", "/apply", tc.body, a.id, "owner", nil)
			if status != 403 || result["error"] != "reauthentication_failed" || calls.Load() != 0 {
				t.Fatalf("%d %v calls=%d", status, result, calls.Load())
			}
		})
	}
}

func TestServerUpdateControlHostFailuresNeverSucceed(t *testing.T) {
	a, payload := serverUpdateTestIdentity(t)
	for _, tc := range []struct {
		name, path, body string
		hostStatus, want int
	}{
		{"unapproved", "/apply", `{"error":"not_approved"}`, 403, 403},
		{"blocked", "/apply", `{"error":"blocked"}`, 409, 409},
		{"persistence", "/apply", `{"error":"persistence_failure"}`, 500, 500},
		{"invalid request", "/apply", `{"error":"invalid_request"}`, 400, 400},
		{"wrong success code", "/apply", serverUpdateTestState("release-one", "succeeded"), 200, 502},
		{"wrong operation", "/apply", serverUpdateTestState("other", "succeeded"), 202, 502},
		{"no operation", "/apply", `{"version":1,"consumed":[],"operation":null}`, 202, 502},
		{"malformed", "/apply", `{`, 202, 502},
		{"redirect", "/apply", `{"error":"private-upstream-secret"}`, 307, 502},
		{"raw error", "/apply", `{"error":"private-upstream-secret"}`, 500, 502},
		{"host API missing", "/status", `{"error":"not_found"}`, 404, 503},
		{"empty status", "/status", `{}`, 200, 502},
		{"future protocol", "/status", `{"version":2,"consumed":[],"operation":null}`, 200, 502},
		{"oversized", "/status", strings.Repeat("x", serverUpdateMaxOutput+1), 200, 502},
		{"missing preflight OK", "/preflight", `{"reason":""}`, 200, 502},
		{"contradictory preflight", "/preflight", `{"ok":true,"reason":"not_approved"}`, 200, 502},
		{"null preflight", "/preflight", `{"ok":null,"reason":"not_approved"}`, 200, 502},
		{"duplicate field", "/preflight", `{"ok":false,"ok":true,"reason":""}`, 200, 502},
		{"nested secret", "/status", strings.Replace(serverUpdateTestState("release-one", "succeeded"), `"rollback_compatible":true`, `"rollback_compatible":true,"password":"private-upstream-secret"`, 1), 200, 502},
		{"nested duplicate", "/status", strings.Replace(serverUpdateTestState("release-one", "succeeded"), `"maintenance":true`, `"maintenance":false,"maintenance":true`, 1), 200, 502},
		{"journal field", "/status", strings.Replace(serverUpdateTestState("release-one", "succeeded"), `"version":1`, `"version":1,"high_water":2`, 1), 200, 502},
		{"journal operation field", "/apply", strings.Replace(serverUpdateTestState("release-one", "succeeded"), `"phase":"succeeded"`, `"phase":"succeeded","sequence":2`, 1), 202, 502},
		{"nested case alias", "/status", strings.Replace(serverUpdateTestState("release-one", "succeeded"), `"maintenance"`, `"Maintenance"`, 1), 200, 502},
		{"null recovery flag", "/apply", strings.Replace(serverUpdateTestState("release-one", "manual_recovery"), `"migration_uncertain":true`, `"migration_uncertain":null`, 1), 202, 502},
		{"unknown recovery error", "/apply", strings.Replace(serverUpdateTestState("release-one", "manual_recovery"), `"apply_failed"`, `"recovery_unknown"`, 1), 202, 502},
		{"unconsumed approval", "/apply", strings.Replace(serverUpdateTestState("release-one", "succeeded"), `"consumed":["release-one"]`, `"consumed":[]`, 1), 202, 502},
		{"mutable image", "/status", strings.Replace(serverUpdateTestState("release-one", "succeeded"), `ghcr.io/proxima/api@sha256:`+strings.Repeat("a", 64), "ghcr.io/proxima/api:latest", 1), 200, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://updater/v1/apply")
				w.WriteHeader(tc.hostStatus)
				_, _ = io.WriteString(w, tc.body)
			}))
			method, body := "GET", ""
			if tc.path == "/apply" {
				method, body = "POST", payload
			}
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), method, tc.path, body, a.id, "owner", nil)
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if status != tc.want || calls.Load() != 1 || strings.Contains(string(encoded), "private-upstream-secret") {
				t.Fatalf("%d %s calls=%d", status, encoded, calls.Load())
			}
			if tc.path == "/apply" && status == 502 && (result["outcome"] != "unknown" || result["check_status"] != true) {
				t.Fatalf("uncertain apply hidden: %v", result)
			}
		})
	}
}

func TestServerUpdateControlUnavailableAndBoundedDeadline(t *testing.T) {
	a, payload := serverUpdateTestIdentity(t)
	for _, path := range []string{"relative.sock", "/tmp/proxima-nonexistent-updater.sock", "/tmp/../tmp/updater.sock"} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("PROXIMA_UPDATER_SOCKET", path)
			status, result := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), "POST", "/apply", payload, a.id, "owner", nil)
			if status != 503 || result["outcome"] != "unknown" || result["check_status"] != true {
				t.Fatalf("%d %v", status, result)
			}
		})
	}
	t.Run("host accepted then lost response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		closed := make(chan struct{})
		t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if _, err := io.ReadAll(r.Body); err != nil {
				t.Error(err)
			}
			cancel() // Simulate cancellation only after the host received apply.
			<-r.Context().Done()
			close(closed)
		}))
		status, body := serverUpdateTestRequest(t, serverUpdateTestHandler(t, a), "POST", "/apply", payload, a.id, "owner", ctx)
		if status != 503 || body["outcome"] != "unknown" || calls.Load() != 1 {
			t.Fatalf("%d %v calls=%d", status, body, calls.Load())
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("upstream request not cancelled")
		}
	})
	t.Run("configured HTTP timeout", func(t *testing.T) {
		closed := make(chan struct{})
		t.Setenv("PROXIMA_UPDATER_SOCKET", serverUpdateTestSocket(t, func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			close(closed)
		}))
		h := serverUpdateTestHandler(t, a)
		// Exercise the production deadline itself, not a wall-clock sleep or
		// polling assertion. app.Test's 10s bound catches a missing 5s timeout.
		status, body := serverUpdateTestRequest(t, h, "GET", "/status", "", a.id, "owner", nil)
		if status != 503 || body["error"] != "updater_unavailable" {
			t.Fatalf("%d %v", status, body)
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("deadline did not close upstream request")
		}
	})
}
