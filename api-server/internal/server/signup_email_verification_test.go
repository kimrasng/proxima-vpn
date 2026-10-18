package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
)

func TestSignupCodeLookupRequiresExplicitFakeMail(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret"}, SMTP: config.SMTPConfig{Host: "smtp.example.test", Port: 587, From: "test@example.test", TLS: "starttls"}}
	for _, enabled := range []string{"", "false", "true"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv("ENABLE_TEST_EMAIL_CODE_LOOKUP", enabled)
			s := NewServer(cfg, nil, nil, nil, nil)
			defer func() { _ = s.App().Shutdown() }()
			_, fake := s.mailer.(*testSignupMailer)
			if fake != (enabled == "true") {
				t.Fatalf("fake sender=%v enabled=%q", fake, enabled)
			}
			req := httptest.NewRequest("GET", "/api/v1/__test__/signup-email-code?email=test@example.test", nil)
			resp, err := s.App().Test(req, -1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			want := 404
			if enabled == "true" {
				want = 401
			}
			if resp.StatusCode != want {
				t.Fatalf("status=%d want=%d", resp.StatusCode, want)
			}
		})
	}
}

func TestSignupCodeLookupAdminOnlyAndBeforeOtherAuthRoutes(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var adminID string
	if err := db.QueryRow(ctx, `INSERT INTO admins(email,password_hash) VALUES($1,'unused') RETURNING id::text`, "signup-admin-"+time.Now().Format("150405.000000000")+"@example.test").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM admins WHERE id=$1`, adminID) }()
	t.Setenv("SWAGGER_ENABLED", "false")
	t.Setenv("ENABLE_TEST_EMAIL_CODE_LOOKUP", "true")
	const secret = "signup-route-secret"
	s := NewServer(&config.Config{JWT: config.JWTConfig{Secret: secret}}, db, nil, nil, nil)
	defer func() { _ = s.App().Shutdown() }()
	fake := s.mailer.(*testSignupMailer)
	if err := fake.Send(ctx, mail.Message{To: "signup@example.test", Subject: "Email verification", Body: "123456"}); err != nil {
		t.Fatal(err)
	}
	adminToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, handlers.AdminClaims{AdminID: adminID, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, handlers.UserClaims{UserID: adminID, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token string
		want        int
	}{{"anonymous", "", 401}, {"user JWT", userToken, 401}, {"admin JWT", adminToken, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/__test__/signup-email-code?email=SIGNUP@example.test", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			resp, err := s.App().Test(req, -1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d", resp.StatusCode, tc.want)
			}
			if tc.want == 200 {
				var body map[string]string
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["code"] != "123456" || resp.Header.Get("Cache-Control") != "no-store" {
					t.Fatalf("code/cache incorrect: %v", body)
				}
			}
		})
	}
}

func TestSignupRequestAndVerificationHaveSeparateLimits(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	s := NewServer(&config.Config{}, nil, nil, nil, nil)
	defer func() { _ = s.App().Shutdown() }()
	request := func(path string) int {
		req := httptest.NewRequest(fiber.MethodPost, "/api/v1/auth/"+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := s.App().Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		return res.StatusCode
	}
	for i := 0; i < 6; i++ {
		want := 503
		if i >= 5 {
			want = 429
		}
		if got := request("register/request-code"); got != want {
			t.Fatalf("send %d: %d want %d", i, got, want)
		}
	}
	for i := 0; i < 11; i++ {
		path := "register/verify-code"
		if i%2 == 0 {
			path = "register"
		}
		want := 503
		if i >= 10 {
			want = 429
		}
		if got := request(path); got != want {
			t.Fatalf("verify %d: %d want %d", i, got, want)
		}
	}
}
