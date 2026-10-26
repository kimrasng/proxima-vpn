package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type clientAPIFixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	app      *fiber.App
	svc      *services.OAuthService
	userID   string
	otherID  string
	category string
	client   *services.OAuthClient
}

func newClientAPIFixture(t *testing.T) *clientAPIFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	t.Setenv("SWAGGER_ENABLED", "false")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	seedUser := func() string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, name, password_hash, sub_token, status, is_active)
			 VALUES (gen_random_uuid()::text || '@client-api.test', 'Before', 'x', 'CLIENTAPI' || upper(substr(md5(random()::text), 1, 20)), 'active', true)
			 RETURNING id::text`).Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		return id
	}
	f := &clientAPIFixture{t: t, pool: pool, svc: services.NewOAuthService(pool), userID: seedUser(), otherID: seedUser()}
	if err := pool.QueryRow(ctx,
		`INSERT INTO support_categories (slug, names) VALUES ('client-api-' || substr(md5(random()::text), 1, 8), '{"en":"Client API"}') RETURNING id::text`,
	).Scan(&f.category); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	active := true
	f.client, _, err = f.svc.CreateClient(ctx, services.OAuthClientInput{
		Name: "client-api-test", ClientType: services.OAuthClientPublic,
		RedirectURIs: []string{"proxima://client-api"}, AllowedScopes: services.OAuthScopes, IsActive: &active,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_clients WHERE id = $1`, f.client.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM support_tickets WHERE user_id IN ($1, $2)`, f.userID, f.otherID)
		_, _ = pool.Exec(ctx, `DELETE FROM support_categories WHERE id = $1`, f.category)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, f.userID, f.otherID)
	})

	s := NewServer(&config.Config{JWT: config.JWTConfig{Secret: "client-api-test-secret"}}, pool, nil, nil, nil)
	t.Cleanup(func() { _ = s.App().Shutdown() })
	f.app = s.App()
	return f
}

func (f *clientAPIFixture) token(userID string, scopes ...string) string {
	f.t.Helper()
	ctx := context.Background()
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	req, err := f.svc.ValidateAuthorize(ctx, services.AuthorizeParams{
		ResponseType: "code", ClientID: f.client.ClientID, RedirectURI: "proxima://client-api",
		Scope: strings.Join(scopes, " "), CodeChallenge: challenge, CodeChallengeMethod: "S256",
	})
	if err != nil {
		f.t.Fatalf("authorize %v: %v", scopes, err)
	}
	code, err := f.svc.Approve(ctx, userID, req)
	if err != nil {
		f.t.Fatalf("approve: %v", err)
	}
	set, err := f.svc.ExchangeCode(ctx, f.client, code, "proxima://client-api", verifier)
	if err != nil {
		f.t.Fatalf("exchange: %v", err)
	}
	return set.AccessToken
}

func (f *clientAPIFixture) call(method, path, token string, body any) (int, []byte) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	if token != "" {
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	}
	res, err := f.app.Test(req, -1)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

func TestClientAPISupportTicketFlow(t *testing.T) {
	f := newClientAPIFixture(t)
	readOnly := f.token(f.userID, services.ScopeSupportRead)
	full := f.token(f.userID, services.ScopeSupportRead, services.ScopeSupportWrite)

	if status, _ := f.call("POST", "/api/v1/client/support/tickets", readOnly, map[string]string{
		"category_id": f.category, "subject": "x", "body": "y",
	}); status != fiber.StatusForbidden {
		t.Fatalf("support:read alone must not open a ticket, got %d", status)
	}

	status, raw := f.call("POST", "/api/v1/client/support/tickets", full, map[string]string{
		"category_id": f.category, "subject": "앱에서 보낸 문의", "body": "첫 메시지",
	})
	if status != fiber.StatusCreated {
		t.Fatalf("create ticket: %d %s", status, raw)
	}
	var created struct{ ID string }
	_ = json.Unmarshal(raw, &created)

	if status, raw := f.call("POST", "/api/v1/client/support/tickets/"+created.ID+"/messages", full, map[string]string{"body": "추가 답장"}); status/100 != 2 {
		t.Fatalf("reply: %d %s", status, raw)
	}

	status, raw = f.call("GET", "/api/v1/client/support/tickets/"+created.ID, readOnly, nil)
	if status != fiber.StatusOK || !bytes.Contains(raw, []byte("첫 메시지")) || !bytes.Contains(raw, []byte("추가 답장")) {
		t.Fatalf("thread must hold both messages: %d %s", status, raw)
	}
	if status, raw := f.call("GET", "/api/v1/client/support/tickets", readOnly, nil); status != fiber.StatusOK || !bytes.Contains(raw, []byte(created.ID)) {
		t.Fatalf("list tickets: %d %s", status, raw)
	}

	otherToken := f.token(f.otherID, services.ScopeSupportRead, services.ScopeSupportWrite)
	if status, _ := f.call("GET", "/api/v1/client/support/tickets/"+created.ID, otherToken, nil); status != fiber.StatusNotFound {
		t.Fatalf("another user must not read this ticket, got %d", status)
	}
	if status, _ := f.call("POST", "/api/v1/client/support/tickets/"+created.ID+"/messages", otherToken, map[string]string{"body": "x"}); status != fiber.StatusNotFound {
		t.Fatalf("another user must not reply to this ticket, got %d", status)
	}

	if status, raw := f.call("POST", "/api/v1/client/support/tickets/"+created.ID+"/close", full, nil); status/100 != 2 {
		t.Fatalf("close: %d %s", status, raw)
	}
	var ticketStatus string
	if err := f.pool.QueryRow(context.Background(), `SELECT status FROM support_tickets WHERE id = $1`, created.ID).Scan(&ticketStatus); err != nil || ticketStatus != "closed" {
		t.Fatalf("ticket must be closed, got %q %v", ticketStatus, err)
	}
}

func TestClientAPIOrdersAndScopes(t *testing.T) {
	f := newClientAPIFixture(t)
	orders := f.token(f.userID, services.ScopeOrdersRead)
	if status, raw := f.call("GET", "/api/v1/client/orders", orders, nil); status != fiber.StatusOK || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		t.Fatalf("orders list: %d %s", status, raw)
	}
	if status, _ := f.call("GET", "/api/v1/client/plans", orders, nil); status != fiber.StatusOK {
		t.Fatalf("plans: %d", status)
	}
	if status, _ := f.call("POST", "/api/v1/client/orders", orders, map[string]any{}); status != fiber.StatusForbidden {
		t.Fatalf("orders:read alone must not create an order, got %d", status)
	}
	if status, _ := f.call("GET", "/api/v1/client/devices", orders, nil); status != fiber.StatusForbidden {
		t.Fatalf("orders:read must not read devices, got %d", status)
	}

	content := f.token(f.userID, services.ScopeContentRead)
	for _, path := range []string{"/api/v1/client/announcements", "/api/v1/client/guides"} {
		if status, raw := f.call("GET", path, content, nil); status != fiber.StatusOK {
			t.Fatalf("%s: %d %s", path, status, raw)
		}
	}
	devices := f.token(f.userID, services.ScopeDevicesRead)
	if status, raw := f.call("GET", "/api/v1/client/devices", devices, nil); status != fiber.StatusOK {
		t.Fatalf("devices: %d %s", status, raw)
	}

	for _, path := range []string{"/api/v1/client/orders", "/api/v1/client/support/tickets", "/api/v1/client/devices"} {
		if status, _ := f.call("GET", path, "", nil); status != fiber.StatusUnauthorized {
			t.Fatalf("%s without a token must be 401, got %d", path, status)
		}
	}
}

func TestClientAPIProfileWriteCannotChangeCredentials(t *testing.T) {
	f := newClientAPIFixture(t)
	write := f.token(f.userID, services.ScopeProfile, services.ScopeProfileWrite)
	var before string
	if err := f.pool.QueryRow(context.Background(), `SELECT email || '|' || password_hash FROM users WHERE id = $1`, f.userID).Scan(&before); err != nil {
		t.Fatalf("read user: %v", err)
	}

	if status, raw := f.call("PUT", "/api/v1/client/profile", write, map[string]string{"name": "After", "language": "en"}); status != fiber.StatusOK {
		t.Fatalf("name/language update: %d %s", status, raw)
	}
	for _, body := range []map[string]string{
		{"email": "attacker@example.com"},
		{"password": "new-password-123"},
		{"name": "x", "email": "attacker@example.com"},
		{"Email": "attacker@example.com"},
		{"PASSWORD": "new-password-123"},
		{"name": "x", "eMail": "attacker@example.com"},
		{"nickname": "x"},
	} {
		if status, _ := f.call("PUT", "/api/v1/client/profile", write, body); status != fiber.StatusForbidden {
			t.Fatalf("%v must be refused with 403, got %d", body, status)
		}
	}
	var name, lang, after string
	if err := f.pool.QueryRow(context.Background(), `SELECT name, language, email || '|' || password_hash FROM users WHERE id = $1`, f.userID).Scan(&name, &lang, &after); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if name != "After" || lang != "en" || after != before {
		t.Fatalf("got name=%q lang=%q credentials changed=%v", name, lang, after != before)
	}

	readOnly := f.token(f.userID, services.ScopeProfile)
	if status, _ := f.call("PUT", "/api/v1/client/profile", readOnly, map[string]string{"name": "x"}); status != fiber.StatusForbidden {
		t.Fatalf("profile alone must not edit, got %d", status)
	}
}

func TestClientAPIDevicesHideConnectionSecretsWithoutSubscriptionScope(t *testing.T) {
	f := newClientAPIFixture(t)
	ctx := context.Background()
	seedDevice := func(userID string) (id, xray string) {
		if err := f.pool.QueryRow(ctx,
			`INSERT INTO devices (user_id, name, xray_uuid) VALUES ($1, 'Phone', gen_random_uuid()::text) RETURNING id::text, xray_uuid`, userID,
		).Scan(&id, &xray); err != nil {
			t.Fatalf("seed device: %v", err)
		}
		return id, xray
	}
	mine, myXray := seedDevice(f.userID)
	theirs, _ := seedDevice(f.otherID)
	var subToken string
	if err := f.pool.QueryRow(ctx, `SELECT sub_token FROM users WHERE id = $1`, f.userID).Scan(&subToken); err != nil {
		t.Fatalf("read sub token: %v", err)
	}

	status, raw := f.call("GET", "/api/v1/client/devices", f.token(f.userID, services.ScopeDevicesRead), nil)
	if status != fiber.StatusOK || !bytes.Contains(raw, []byte(mine)) {
		t.Fatalf("devices:read must list the user's device: %d %s", status, raw)
	}
	if bytes.Contains(raw, []byte(theirs)) {
		t.Fatalf("another user's device leaked: %s", raw)
	}
	if bytes.Contains(raw, []byte(subToken)) || bytes.Contains(raw, []byte(myXray)) {
		t.Fatalf("devices:read alone must not expose the sub token or xray uuid: %s", raw)
	}

	status, raw = f.call("GET", "/api/v1/client/devices", f.token(f.userID, services.ScopeDevicesRead, services.ScopeSubscriptionRead), nil)
	if status != fiber.StatusOK || !bytes.Contains(raw, []byte(subToken)) || !bytes.Contains(raw, []byte(myXray)) {
		t.Fatalf("with subscription:read the connection details must be returned: %d %s", status, raw)
	}

	otherWrite := f.token(f.otherID, services.ScopeDevicesWrite)
	if status, _ := f.call("DELETE", "/api/v1/client/devices/"+mine, otherWrite, nil); status != fiber.StatusNotFound {
		t.Fatalf("another user must not delete this device, got %d", status)
	}
	var retired bool
	if err := f.pool.QueryRow(ctx, `SELECT retired_at IS NOT NULL FROM devices WHERE id = $1`, mine).Scan(&retired); err != nil || retired {
		t.Fatalf("device must stay active, retired=%v err=%v", retired, err)
	}
}
