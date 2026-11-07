package server

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
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"golang.org/x/crypto/bcrypt"
)

func TestServerReleaseRouteRequiresAuthentication(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	s := NewServer(&config.Config{}, nil, nil, nil, nil)
	defer func() { _ = s.App().Shutdown() }()
	for _, route := range []struct {
		method string
		path   string
	}{
		{fiber.MethodGet, "/api/v1/admin/system-updates/latest?owner=kimrasng&repo=proxima-vpn"},
		{fiber.MethodGet, "/api/v1/admin/system-updates/prepare"},
		{fiber.MethodPost, "/api/v1/admin/system-updates/prepare"},
		{fiber.MethodPost, "/api/v1/admin/system-updates/apply"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			resp, err := s.App().Test(httptest.NewRequest(route.method, route.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestAdminLoginRouteLimitsEleventhAttempt(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	s := NewServer(&config.Config{}, nil, nil, nil, nil)
	defer func() { _ = s.App().Shutdown() }()

	// Missing credentials exercise the real login handler without querying a DB.
	// Eleven requests stay below the separate global limit of 100 requests.
	for attempt := 1; attempt <= 11; attempt++ {
		req := httptest.NewRequest(fiber.MethodPost, "/api/v1/admin/auth/login", strings.NewReader(`{}`))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		resp, err := s.App().Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error string `json:"error"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		closeErr := resp.Body.Close()
		if decodeErr != nil {
			t.Fatalf("attempt %d: decode response: %v", attempt, decodeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		wantStatus, wantError := fiber.StatusBadRequest, "email and password are required"
		if attempt == 11 {
			wantStatus, wantError = fiber.StatusTooManyRequests, "too many login attempts, try again later"
		}
		if resp.StatusCode != wantStatus || body.Error != wantError {
			t.Fatalf("attempt %d: status = %d, error = %q; want %d, %q", attempt, resp.StatusCode, body.Error, wantStatus, wantError)
		}
	}
}

func TestReadinessFailsWithoutDependencies(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	s := NewServer(&config.Config{}, nil, nil, nil, nil)
	defer func() { _ = s.App().Shutdown() }()
	resp, err := s.App().Test(httptest.NewRequest(fiber.MethodGet, "/ready", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestServerUpdateRoutesRequireOwnerAndLimitEleventhAttempt(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required; use a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("route-owner-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "server-update-route-test-secret"
	const otpSecret = "JBSWY3DPEHPK3PXP"
	tokens := make(map[string]string)
	for _, role := range []string{"owner", "admin"} {
		var id string
		var changed time.Time
		if err := db.QueryRow(ctx, `INSERT INTO admins
			(email, password_hash, role, is_active, totp_secret, totp_enabled)
			VALUES (gen_random_uuid()::text || '@update-route.test', $1, $2, true, $3, true)
			RETURNING id::text, password_changed_at`, string(hash), role, otpSecret).Scan(&id, &changed); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := db.Exec(context.Background(), `DELETE FROM admins WHERE id=$1`, id); err != nil {
				t.Error(err)
			}
		})
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, handlers.AdminClaims{
			AdminID: id, Role: "owner", PasswordChangedAt: changed.UnixMicro(),
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		// Even a token claiming owner must use the current role from PostgreSQL.
		tokens[role] = token
	}

	// A short Unix socket path also works on macOS. This host only records
	// requests and returns protocol responses; it never performs an update.
	dir, err := os.MkdirTemp("/tmp", "route-up-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	host := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/prepare":
			if string(body) != `{"tag":"v1.2.3"}` {
				t.Errorf("unsafe prepare payload: %s", body)
			}
			w.WriteHeader(http.StatusAccepted)
			_, err = io.WriteString(w, `{"status":"preparing","tag":"v1.2.3"}`)
		case "POST /v1/apply":
			if string(body) != `{"release_id":"release-one","maintenance":true}` {
				t.Errorf("unsafe apply payload: %s", body)
			}
			// A host rejection still proves valid owner reauthentication reached
			// the updater. It carries no actual update or production side effect.
			w.WriteHeader(http.StatusConflict)
			_, err = io.WriteString(w, `{"error":"blocked"}`)
		default:
			t.Errorf("unexpected host request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
		if err != nil {
			t.Error(err)
		}
	})}
	done := make(chan error, 1)
	go func() { done <- host.Serve(listener) }()
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})
	t.Setenv("PROXIMA_UPDATER_SOCKET", socket)
	t.Setenv("SWAGGER_ENABLED", "false")
	for _, route := range []struct {
		name, fields, response string
		status                 int
	}{
		{"prepare", `"tag":"v1.2.3"`, `{"status":"preparing","tag":"v1.2.3"}`, fiber.StatusAccepted},
		{"apply", `"release_id":"release-one","maintenance":true`, `{"error":"blocked"}`, fiber.StatusConflict},
	} {
		t.Run(route.name, func(t *testing.T) {
			s := NewServer(&config.Config{JWT: config.JWTConfig{Secret: secret}}, db, nil, nil, nil)
			t.Cleanup(func() {
				if err := s.App().Shutdown(); err != nil {
					t.Error(err)
				}
			})
			before := calls.Load()
			for _, role := range []string{"admin", "owner"} {
				for attempt := 1; attempt <= 11; attempt++ {
					code, err := totp.GenerateCode(otpSecret, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					payload := `{` + route.fields + `,"password":"route-owner-password","totp_code":"` + code + `"}`
					req := httptest.NewRequest(fiber.MethodPost, "/api/v1/admin/system-updates/"+route.name, strings.NewReader(payload))
					req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
					req.Header.Set(fiber.HeaderAuthorization, "Bearer "+tokens[role])
					resp, err := s.App().Test(req, 10000)
					if err != nil {
						t.Fatal(err)
					}
					body, readErr := io.ReadAll(resp.Body)
					closeErr := resp.Body.Close()
					if readErr != nil || closeErr != nil {
						t.Fatalf("read=%v close=%v", readErr, closeErr)
					}
					wantStatus, wantBody := route.status, route.response
					wantCalls := before + int32(attempt)
					if role == "admin" {
						wantStatus, wantBody = fiber.StatusForbidden, `{"error":"only an owner can manage admin accounts"}`
						wantCalls = before
					} else if attempt == 11 {
						wantStatus, wantBody = fiber.StatusTooManyRequests, `{"error":"too many login attempts, try again later"}`
						wantCalls = before + 10
					}
					if resp.StatusCode != wantStatus || string(body) != wantBody || calls.Load() != wantCalls {
						t.Fatalf("%s attempt %d: status=%d body=%s calls=%d; want %d %s calls=%d", role, attempt, resp.StatusCode, body, calls.Load(), wantStatus, wantBody, wantCalls)
					}
				}
			}
		})
	}
}
