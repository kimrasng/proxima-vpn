package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
	"github.com/proximavpn/proxima-vpn/api-server/internal/middleware"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

const (
	pwrPanelURL    = "https://panel.example.com"
	pwrJWTSecret   = "password-reset-test-secret"
	pwrOldPassword = "OldPassw0rd!"
	pwrNewPassword = "BrandNewPassw0rd!"
)

// pwrMailer records what the handler would have emailed.
type pwrMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
	// gate, when set, blocks Send until it is closed.
	gate chan struct{}
}

func (m *pwrMailer) Send(_ context.Context, msg mail.Message) error {
	if m.gate != nil {
		<-m.gate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return m.err
}

func (m *pwrMailer) messages() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mail.Message(nil), m.sent...)
}

type pwrFixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	app    *fiber.App
	h      *handlers.UserAuthHandler
	mailer *pwrMailer
	// prefix is where the auth routes are mounted: empty for the handler-only
	// app, /api/v1 for the real server.
	prefix string
}

func pwrDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed password reset tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newPwrFixture builds the handler against the real database and a fake mailer.
// A nil mailer models "SMTP not configured".
func newPwrFixture(t *testing.T, mailer *pwrMailer, panelURL string) *pwrFixture {
	t.Helper()
	pool := pwrDB(t)

	var sender mail.Sender
	if mailer != nil {
		sender = mailer
	}
	h := handlers.NewUserAuthHandler(pool, pwrJWTSecret, time.Hour,
		services.NewTelegramService(pool, config.TelegramConfig{}), sender, panelURL)

	app := fiber.New()
	app.Post("/auth/forgot-password", h.ForgotPassword)
	app.Post("/auth/reset-password", h.ResetPassword)
	app.Post("/auth/login", h.Login)
	protected := app.Group("/user")
	protected.Use(middleware.UserJWTMiddleware(pwrJWTSecret, pool))
	protected.Get("/ping", func(c *fiber.Ctx) error { return c.SendString("pong") })

	t.Cleanup(func() { h.WaitForPasswordResetWork() })
	return &pwrFixture{t: t, pool: pool, app: app, h: h, mailer: mailer}
}

// seedUser inserts a user with a real password hash and registers cleanup.
func (f *pwrFixture) seedUser(status string, active bool) (id, email string) {
	f.t.Helper()
	email = fmt.Sprintf("pwreset-%s@example.test", crypto.NewUUID())
	hash, err := crypto.HashPassword(pwrOldPassword)
	if err != nil {
		f.t.Fatalf("hash: %v", err)
	}
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, name, sub_token, status, is_active)
		 VALUES ($1, $2, 'Reset Test', $3, $4, $5) RETURNING id::text`,
		email, hash, crypto.NewUUID(), status, active,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	f.t.Cleanup(func() {
		ctx := context.Background()
		_, _ = f.pool.Exec(ctx, `DELETE FROM activity_logs WHERE target_id = $1 OR actor_id = $1`, id)
		_, _ = f.pool.Exec(ctx, `DELETE FROM login_history WHERE attempted_email = $1`, email)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) // tokens cascade
	})
	return id, email
}

type pwrResponse struct {
	status int
	body   []byte
}

func (r pwrResponse) field(t *testing.T, key string) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
	return m[key]
}

func (f *pwrFixture) post(path string, body any, headers ...string) pwrResponse {
	f.t.Helper()
	var payload []byte
	switch v := body.(type) {
	case string:
		payload = []byte(v)
	default:
		var err error
		if payload, err = json.Marshal(v); err != nil {
			f.t.Fatalf("marshal: %v", err)
		}
	}
	req := httptest.NewRequest(fiber.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := f.app.Test(req, 10_000)
	if err != nil {
		f.t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return pwrResponse{status: res.StatusCode, body: b}
}

func (f *pwrFixture) get(path, bearer string) int {
	f.t.Helper()
	req := httptest.NewRequest(fiber.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := f.app.Test(req, 10_000)
	if err != nil {
		f.t.Fatalf("GET %s: %v", path, err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// forgot requests a reset and waits for the background work to finish.
func (f *pwrFixture) forgot(email string, extra ...string) pwrResponse {
	f.t.Helper()
	res := f.post("/auth/forgot-password", map[string]string{"email": email}, extra...)
	f.h.WaitForPasswordResetWork()
	return res
}

func (f *pwrFixture) reset(token, password string) pwrResponse {
	f.t.Helper()
	return f.post("/auth/reset-password", map[string]string{"token": token, "password": password})
}

func (f *pwrFixture) login(email, password string) (int, string) {
	f.t.Helper()
	res := f.post(f.prefix+"/auth/login", map[string]string{"email": email, "password": password})
	return res.status, res.field(f.t, "token")
}

func (f *pwrFixture) tokenRows(userID string) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = $1`, userID).Scan(&n); err != nil {
		f.t.Fatalf("count tokens: %v", err)
	}
	return n
}

func (f *pwrFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// tokenFromMail extracts the raw token from the single link in an email body.
func tokenFromMail(t *testing.T, msg mail.Message) (token, link string) {
	t.Helper()
	for _, field := range strings.Fields(msg.Body) {
		if strings.HasPrefix(field, "http") {
			link = field
		}
	}
	if link == "" {
		t.Fatalf("no link in body:\n%s", msg.Body)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	token = u.Query().Get("token")
	if token == "" {
		t.Fatalf("link %q carries no token", link)
	}
	return token, link
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// requestResetToken runs forgot-password for a seeded user and returns the raw
// token that was emailed.
func (f *pwrFixture) requestResetToken(email string) string {
	f.t.Helper()
	before := len(f.mailer.messages())
	if res := f.forgot(email); res.status != fiber.StatusOK {
		f.t.Fatalf("forgot-password status = %d (%s)", res.status, res.body)
	}
	msgs := f.mailer.messages()
	if len(msgs) != before+1 {
		f.t.Fatalf("emails sent = %d, want %d", len(msgs), before+1)
	}
	token, _ := tokenFromMail(f.t, msgs[len(msgs)-1])
	return token
}

func TestForgotPassword_SameResponseForKnownAndUnknownAccounts(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	_, known := f.seedUser("active", true)
	suspendedID, suspended := f.seedUser("suspended", true)
	inactiveID, inactive := f.seedUser("active", false)
	unknown := fmt.Sprintf("nobody-%s@example.test", crypto.NewUUID())

	want := f.forgot(known)
	if want.status != fiber.StatusOK || want.field(t, "message") == "" {
		t.Fatalf("known account: status %d body %s", want.status, want.body)
	}

	for name, email := range map[string]string{"unknown": unknown, "suspended": suspended, "inactive": inactive} {
		got := f.forgot(email)
		if got.status != want.status || !bytes.Equal(got.body, want.body) {
			t.Errorf("%s: response %d %s differs from known account's %d %s", name, got.status, got.body, want.status, want.body)
		}
	}
	// A second request inside the cooldown must look identical too.
	if again := f.forgot(known); again.status != want.status || !bytes.Equal(again.body, want.body) {
		t.Errorf("throttled request: %d %s differs from %d %s", again.status, again.body, want.status, want.body)
	}

	msgs := f.mailer.messages()
	if len(msgs) != 1 || msgs[0].To != known {
		t.Fatalf("emails = %+v, want exactly one, to the known account", msgs)
	}
	if n := f.tokenRows(suspendedID) + f.tokenRows(inactiveID); n != 0 {
		t.Errorf("suspended/inactive accounts got %d token rows, want 0", n)
	}
}

func TestForgotPassword_StoresOnlyTheHashAndLinksToTheConfiguredPanel(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL+"/") // trailing slash must be trimmed
	userID, email := f.seedUser("active", true)

	// A hostile Host / X-Forwarded-* must have no say in the link.
	res := f.forgot(email,
		"Host", "evil.example",
		"X-Forwarded-Host", "evil.example",
		"X-Forwarded-Proto", "http",
		"Origin", "https://evil.example")
	if res.status != fiber.StatusOK {
		t.Fatalf("status %d: %s", res.status, res.body)
	}

	msgs := f.mailer.messages()
	if len(msgs) != 1 {
		t.Fatalf("emails = %d, want 1", len(msgs))
	}
	token, link := tokenFromMail(t, msgs[0])
	if want := pwrPanelURL + "/reset-password?token=" + token; link != want {
		t.Fatalf("link = %q, want %q", link, want)
	}
	if strings.Contains(msgs[0].Body, "evil.example") || strings.Contains(msgs[0].Subject, "evil.example") {
		t.Fatalf("request headers leaked into the email:\n%s", msgs[0].Body)
	}
	if len(token) < 43 {
		t.Errorf("token %q is shorter than 256 bits of base64url", token)
	}

	var rows int
	var hash string
	var expires, created time.Time
	var used *time.Time
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) OVER (), token_hash, expires_at, created_at, used_at
		 FROM password_reset_tokens WHERE user_id = $1`, userID,
	).Scan(&rows, &hash, &expires, &created, &used); err != nil {
		t.Fatalf("read token row: %v", err)
	}
	if rows != 1 || hash != sha256Hex(token) || used != nil {
		t.Fatalf("rows=%d hash-ok=%v used=%v", rows, hash == sha256Hex(token), used)
	}
	if ttl := expires.Sub(created); ttl < 29*time.Minute || ttl > 31*time.Minute {
		t.Errorf("token lifetime = %s, want 30 minutes", ttl)
	}

	var leaked int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM password_reset_tokens
		 WHERE token_hash = $1 OR request_ip = $1`, token).Scan(&leaked); err != nil {
		t.Fatalf("leak check: %v", err)
	}
	if leaked != 0 {
		t.Fatal("the raw token is stored in the table")
	}
}

func TestForgotPassword_PerAccountCooldown(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	userID, email := f.seedUser("active", true)

	first := f.forgot(email)
	second := f.forgot(email)
	if first.status != fiber.StatusOK || second.status != fiber.StatusOK || !bytes.Equal(first.body, second.body) {
		t.Fatalf("responses differ: %d %s / %d %s", first.status, first.body, second.status, second.body)
	}
	if n := len(f.mailer.messages()); n != 1 {
		t.Fatalf("emails inside the cooldown = %d, want 1", n)
	}
	if n := f.tokenRows(userID); n != 1 {
		t.Fatalf("token rows inside the cooldown = %d, want 1", n)
	}

	f.exec(`UPDATE password_reset_tokens SET created_at = NOW() - INTERVAL '61 seconds' WHERE user_id = $1`, userID)
	f.forgot(email)
	if n := len(f.mailer.messages()); n != 2 {
		t.Fatalf("emails after the cooldown = %d, want 2", n)
	}
	if n := f.tokenRows(userID); n != 2 {
		t.Fatalf("token rows after the cooldown = %d, want 2", n)
	}
}

func TestForgotPassword_UsesTheRequestedLanguage(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)

	cases := []struct{ language, subject string }{
		{"ko", "Proxima 비밀번호 재설정 안내"},
		{"zh", "重置您的 Proxima 密码"},
		{"en", "Reset your Proxima password"},
		{"", "Reset your Proxima password"},
		{"fr", "Reset your Proxima password"},
		{"ko-KR", "Proxima 비밀번호 재설정 안내"},
	}
	for _, tc := range cases {
		_, email := f.seedUser("active", true)
		res := f.post("/auth/forgot-password", map[string]string{"email": email, "language": tc.language})
		f.h.WaitForPasswordResetWork()
		if res.status != fiber.StatusOK {
			t.Fatalf("language %q: status %d", tc.language, res.status)
		}
		msgs := f.mailer.messages()
		if got := msgs[len(msgs)-1].Subject; got != tc.subject {
			t.Errorf("language %q: subject %q, want %q", tc.language, got, tc.subject)
		}
	}
}

func TestForgotPassword_RejectsMalformedRequests(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)

	for name, body := range map[string]any{
		"malformed json": `{"email":`,
		"empty body":     ``,
		"no email":       map[string]string{"language": "en"},
		"blank email":    map[string]string{"email": "   "},
		"not an email":   map[string]string{"email": "not-an-email"},
		"crlf in email":  map[string]string{"email": "a@example.com\r\nBcc: b@example.com"},
		"two addresses":  map[string]string{"email": "a@example.com,b@example.com"},
	} {
		res := f.post("/auth/forgot-password", body)
		if res.status != fiber.StatusBadRequest || res.field(t, "error") == "" {
			t.Errorf("%s: status %d body %s, want 400 with an error", name, res.status, res.body)
		}
	}
	if n := len(f.mailer.messages()); n != 0 {
		t.Fatalf("a rejected request sent %d emails", n)
	}
}

func TestForgotPassword_Returns503WhenMailIsNotConfigured(t *testing.T) {
	_, email := newPwrFixture(t, nil, pwrPanelURL).seedUser("active", true)

	for name, f := range map[string]*pwrFixture{
		"no mailer":      newPwrFixture(t, nil, pwrPanelURL),
		"no panel url":   newPwrFixture(t, &pwrMailer{}, ""),
		"unusable panel": newPwrFixture(t, &pwrMailer{}, "javascript:alert(1)"),
	} {
		for _, address := range []string{email, "nobody-" + crypto.NewUUID() + "@example.test"} {
			res := f.post("/auth/forgot-password", map[string]string{"email": address})
			if res.status != fiber.StatusServiceUnavailable ||
				res.field(t, "error") != "password reset email is not configured" {
				t.Errorf("%s / %s: status %d body %s", name, address, res.status, res.body)
			}
		}
	}
}

func TestForgotPassword_ResponseDoesNotWaitForTheMailServer(t *testing.T) {
	gate := make(chan struct{})
	mailer := &pwrMailer{gate: gate}
	f := newPwrFixture(t, mailer, pwrPanelURL)
	_, email := f.seedUser("active", true)

	done := make(chan pwrResponse, 1)
	go func() { done <- f.post("/auth/forgot-password", map[string]string{"email": email}) }()

	select {
	case res := <-done:
		if res.status != fiber.StatusOK {
			t.Fatalf("status %d: %s", res.status, res.body)
		}
	case <-time.After(5 * time.Second):
		close(gate)
		t.Fatal("the HTTP response is blocked on the SMTP send")
	}
	if n := len(mailer.messages()); n != 0 {
		t.Fatalf("mail already sent while the gate is closed: %d", n)
	}
	close(gate)
	f.h.WaitForPasswordResetWork()
	if n := len(mailer.messages()); n != 1 {
		t.Fatalf("emails after release = %d, want 1", n)
	}
}

func TestForgotPassword_MailFailureIsInvisibleAndNeverLogsTheToken(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	mailer := &pwrMailer{err: errors.New("smtp: connection refused")}
	f := newPwrFixture(t, mailer, pwrPanelURL)
	userID, email := f.seedUser("active", true)

	res := f.forgot(email)
	if res.status != fiber.StatusOK {
		t.Fatalf("status %d: %s", res.status, res.body)
	}
	msgs := mailer.messages()
	if len(msgs) != 1 {
		t.Fatalf("emails attempted = %d, want 1", len(msgs))
	}
	token, link := tokenFromMail(t, msgs[0])

	out := logs.String()
	if !strings.Contains(out, "connection refused") {
		t.Errorf("the failure was not logged:\n%s", out)
	}
	for _, secret := range []string{token, link, "reset-password?token"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log output contains %q:\n%s", secret, out)
		}
	}
	if f.tokenRows(userID) != 1 {
		t.Error("the token row should survive a failed send (the cooldown still applies)")
	}
}

func TestResetPassword_SucceedsOnceAndChangesTheLogin(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	userID, email := f.seedUser("active", true)
	token := f.requestResetToken(email)

	res := f.reset(token, pwrNewPassword)
	if res.status != fiber.StatusOK || res.field(t, "message") == "" {
		t.Fatalf("reset: status %d body %s", res.status, res.body)
	}

	if status, _ := f.login(email, pwrOldPassword); status != fiber.StatusUnauthorized {
		t.Errorf("login with the old password = %d, want 401", status)
	}
	if status, jwt := f.login(email, pwrNewPassword); status != fiber.StatusOK || jwt == "" {
		t.Errorf("login with the new password = %d, want 200 with a token", status)
	}

	var usedAt *time.Time
	if err := f.pool.QueryRow(context.Background(),
		`SELECT used_at FROM password_reset_tokens WHERE user_id = $1`, userID).Scan(&usedAt); err != nil || usedAt == nil {
		t.Fatalf("token not marked used: %v %v", usedAt, err)
	}

	second := f.reset(token, "AnotherPassw0rd!")
	if second.status != fiber.StatusBadRequest || second.field(t, "error") != "invalid or expired reset token" {
		t.Fatalf("reuse: status %d body %s", second.status, second.body)
	}
	if status, _ := f.login(email, pwrNewPassword); status != fiber.StatusOK {
		t.Error("a rejected reuse changed the password")
	}

	var events int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM activity_logs WHERE event_type = $1 AND target_id = $2`,
		services.EventUserPasswordReset, userID).Scan(&events); err != nil || events != 1 {
		t.Errorf("password reset activity entries = %d (%v), want 1", events, err)
	}
}

func TestResetPassword_UnknownExpiredAndUsedTokensShareOneAnswer(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	userID, email := f.seedUser("active", true)

	expired := f.requestResetToken(email)
	f.exec(`UPDATE password_reset_tokens SET expires_at = NOW() - INTERVAL '1 minute', created_at = NOW() - INTERVAL '2 minutes' WHERE user_id = $1`, userID)
	used := f.requestResetToken(email)
	if res := f.reset(used, pwrNewPassword); res.status != fiber.StatusOK {
		t.Fatalf("setup reset failed: %d %s", res.status, res.body)
	}

	want := pwrResponse{status: fiber.StatusBadRequest, body: []byte(`{"error":"invalid or expired reset token"}`)}
	for name, token := range map[string]string{
		"unknown":   "this-token-was-never-issued",
		"expired":   expired,
		"used":      used,
		"empty":     "",
		"too long":  strings.Repeat("a", 500),
		"sql shape": "' OR '1'='1",
	} {
		got := f.reset(token, "ValidPassw0rd!")
		if got.status != want.status || !bytes.Equal(got.body, want.body) {
			t.Errorf("%s: %d %s, want %d %s", name, got.status, got.body, want.status, want.body)
		}
	}
	// Neither failure may have changed the password set by the successful reset.
	if status, _ := f.login(email, pwrNewPassword); status != fiber.StatusOK {
		t.Error("a failed reset changed the password")
	}
}

func TestResetPassword_RejectsWeakPasswordsWithoutBurningTheToken(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	_, email := f.seedUser("active", true)
	token := f.requestResetToken(email)

	for name, password := range map[string]string{
		"empty":     "",
		"too short": "short1!",
		"too long":  strings.Repeat("a", 73),
	} {
		res := f.reset(token, password)
		if res.status != fiber.StatusBadRequest || res.field(t, "error") == "" ||
			res.field(t, "error") == "invalid or expired reset token" {
			t.Errorf("%s: status %d body %s, want 400 with a password message", name, res.status, res.body)
		}
	}
	if status, _ := f.login(email, pwrOldPassword); status != fiber.StatusOK {
		t.Error("a rejected password changed the account")
	}
	if res := f.reset(token, "exactly8"); res.status != fiber.StatusOK {
		t.Fatalf("the token should still work after weak-password rejections: %d %s", res.status, res.body)
	}
}

func TestResetPassword_MalformedBody(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	for name, body := range map[string]any{
		"not json": `{"token":`,
		"empty":    ``,
	} {
		if res := f.post("/auth/reset-password", body); res.status != fiber.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, res.status)
		}
	}
}

func TestResetPassword_InvalidatesEveryOtherOutstandingToken(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	userID, email := f.seedUser("active", true)

	first := f.requestResetToken(email)
	f.exec(`UPDATE password_reset_tokens SET created_at = NOW() - INTERVAL '2 minutes' WHERE user_id = $1`, userID)
	second := f.requestResetToken(email)
	f.exec(`UPDATE password_reset_tokens SET created_at = NOW() - INTERVAL '2 minutes' WHERE user_id = $1`, userID)
	third := f.requestResetToken(email)
	if f.tokenRows(userID) != 3 {
		t.Fatalf("token rows = %d, want 3", f.tokenRows(userID))
	}

	if res := f.reset(second, pwrNewPassword); res.status != fiber.StatusOK {
		t.Fatalf("reset: %d %s", res.status, res.body)
	}

	var outstanding int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&outstanding); err != nil || outstanding != 0 {
		t.Fatalf("outstanding tokens after a reset = %d (%v), want 0", outstanding, err)
	}
	for name, token := range map[string]string{"first": first, "third": third} {
		if res := f.reset(token, "YetAnotherPassw0rd!"); res.status != fiber.StatusBadRequest {
			t.Errorf("%s token still works after the reset: %d %s", name, res.status, res.body)
		}
	}
	if status, _ := f.login(email, pwrNewPassword); status != fiber.StatusOK {
		t.Error("the password was changed by a retired token")
	}
}

func TestResetPassword_ConcurrentUsesOfOneTokenSucceedExactlyOnce(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	_, email := f.seedUser("active", true)
	token := f.requestResetToken(email)

	const attempts = 6
	statuses := make(chan int, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- f.reset(token, fmt.Sprintf("ConcurrentPassw0rd-%d", i)).status
		}()
	}
	wg.Wait()
	close(statuses)

	ok, rejected := 0, 0
	for s := range statuses {
		switch s {
		case fiber.StatusOK:
			ok++
		case fiber.StatusBadRequest:
			rejected++
		default:
			t.Errorf("unexpected status %d", s)
		}
	}
	if ok != 1 || rejected != attempts-1 {
		t.Fatalf("ok=%d rejected=%d, want exactly one success", ok, rejected)
	}
}

func TestResetPassword_RefusesSuspendedAccounts(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	userID, email := f.seedUser("active", true)
	token := f.requestResetToken(email)

	f.exec(`UPDATE users SET status = 'suspended' WHERE id = $1`, userID)
	res := f.reset(token, pwrNewPassword)
	if res.status != fiber.StatusBadRequest || res.field(t, "error") != "invalid or expired reset token" {
		t.Fatalf("suspended account: %d %s", res.status, res.body)
	}

	f.exec(`UPDATE users SET status = 'active' WHERE id = $1`, userID)
	if status, _ := f.login(email, pwrOldPassword); status != fiber.StatusOK {
		t.Error("the refused reset changed the password")
	}
}

func TestResetPassword_EndsSessionsThatPredateIt(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	_, email := f.seedUser("active", true)

	status, oldSession := f.login(email, pwrOldPassword)
	if status != fiber.StatusOK {
		t.Fatalf("login: %d", status)
	}
	if got := f.get("/user/ping", oldSession); got != fiber.StatusOK {
		t.Fatalf("session before the reset: %d, want 200", got)
	}

	// JWT iat has one-second resolution; make sure the reset lands in a later
	// second than the login that produced oldSession.
	time.Sleep(1100 * time.Millisecond)

	token := f.requestResetToken(email)
	if res := f.reset(token, pwrNewPassword); res.status != fiber.StatusOK {
		t.Fatalf("reset: %d %s", res.status, res.body)
	}

	if got := f.get("/user/ping", oldSession); got != fiber.StatusUnauthorized {
		t.Errorf("session from before the reset: %d, want 401", got)
	}
	// A login immediately after the reset (same second) must work.
	status, newSession := f.login(email, pwrNewPassword)
	if status != fiber.StatusOK {
		t.Fatalf("login after the reset: %d", status)
	}
	if got := f.get("/user/ping", newSession); got != fiber.StatusOK {
		t.Errorf("session created right after the reset: %d, want 200", got)
	}
}

func TestPasswordReset_NeverResetsAnotherAccount(t *testing.T) {
	f := newPwrFixture(t, &pwrMailer{}, pwrPanelURL)
	_, alice := f.seedUser("active", true)
	_, bob := f.seedUser("active", true)

	token := f.requestResetToken(alice)
	if res := f.reset(token, pwrNewPassword); res.status != fiber.StatusOK {
		t.Fatalf("reset: %d %s", res.status, res.body)
	}
	if status, _ := f.login(bob, pwrOldPassword); status != fiber.StatusOK {
		t.Error("resetting one account changed another's password")
	}
}
