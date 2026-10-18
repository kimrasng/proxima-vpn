package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type signupCodeMailer struct{ messages []mail.Message }

func (m *signupCodeMailer) Send(_ context.Context, msg mail.Message) error {
	m.messages = append(m.messages, msg)
	return nil
}

type signupHTTPResponse struct {
	code int
	body string
}

func signupVerificationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestSignupEmailCodeMustBeVerifiedBeforeAccountCreation(t *testing.T) {
	pool := signupVerificationDB(t)
	ctx := context.Background()
	mailer := &signupCodeMailer{}
	h := handlers.NewUserAuthHandler(pool, "signup-code-test-secret", time.Hour, services.NewTelegramService(pool, config.TelegramConfig{}), mailer, "https://panel.example.test")
	app := fiber.New()
	app.Post("/request", h.RequestSignupEmailCode)
	app.Post("/verify", h.VerifySignupEmailCode)
	email := "signup-code-" + time.Now().Format("150405.000000000") + "@example.test"
	payload := map[string]any{"name": "Code test", "email": email, "password": "SignupCodePass!42", "referral_code": "REF-7"}
	request := func(path string, body any) signupHTTPResponse {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return signupHTTPResponse{res.StatusCode, string(raw)}
	}
	res := request("/request", payload)
	if res.code != 200 || len(mailer.messages) != 1 {
		t.Fatalf("request: status=%d sent=%d body=%s", res.code, len(mailer.messages), res.body)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email=$1`, email).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatal("account created before code verification")
	}
	match := regexp.MustCompile(`\b[0-9]{6}\b`).FindString(mailer.messages[0].Body)
	if match == "" {
		t.Fatalf("verification code missing from mail body %q", mailer.messages[0].Body)
	}
	bad := "000000"
	if match == bad {
		bad = "000001"
	}
	res = request("/verify", map[string]any{"email": email, "code": bad})
	if res.code != 400 {
		t.Fatalf("wrong code status=%d", res.code)
	}
	res = request("/verify", map[string]any{"email": email, "code": match})
	if res.code != fiber.StatusCreated {
		t.Fatalf("verify status=%d body=%s", res.code, res.body)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email=$1`, email).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 1 {
		t.Fatalf("created accounts=%d, want 1", after)
	}
	res = request("/verify", map[string]any{"email": email, "code": match})
	if res.code != 400 {
		t.Fatalf("replay status=%d, want 400", res.code)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, email)
}
