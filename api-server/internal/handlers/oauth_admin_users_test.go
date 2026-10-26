package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
)

type adminUsersFixture struct {
	*oauthFixture
	otherID string
	other   *fiber.App
}

func newAdminUsersFixture(t *testing.T) *adminUsersFixture {
	t.Helper()
	f := newOAuthFixture(t)
	ctx := context.Background()
	var otherID string
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, sub_token, status, is_active)
		 VALUES (gen_random_uuid()::text || '@oauth.test', 'x', 'OAUTHTEST' || upper(substr(md5(random()::text), 1, 20)), 'active', true)
		 RETURNING id::text`).Scan(&otherID); err != nil {
		t.Fatalf("seed second user: %v", err)
	}
	t.Cleanup(func() { _, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherID) })

	h := handlers.NewOAuthHandler(f.pool, "https://panel.test")
	f.app.Get("/admin/oauth-apps/:id/users", h.ListAppUsers)
	f.app.Delete("/admin/oauth-apps/:id/users", h.RevokeAllAppUsers)
	f.app.Delete("/admin/oauth-apps/:id/users/:user_id", h.RevokeAppUser)

	other := fiber.New()
	other.Post("/user/oauth/authorize", func(c *fiber.Ctx) error {
		c.Locals("user_id", otherID)
		return c.Next()
	}, h.AuthorizeDecision)
	t.Cleanup(func() { _ = other.Shutdown() })
	return &adminUsersFixture{oauthFixture: f, otherID: otherID, other: other}
}

func (f *adminUsersFixture) accessTokenAs(app *fiber.App, clientID, secret, redirect string) string {
	f.t.Helper()
	verifier, challenge := pkcePair()
	saved := f.app
	f.app = app
	code := f.approve(clientID, redirect, "profile", challenge)
	f.app = saved
	status, tok := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier}}, basic(clientID, secret))
	if status != 200 {
		f.t.Fatalf("token exchange: %d %v", status, tok)
	}
	return tok["access_token"].(string)
}

func (f *adminUsersFixture) listUsers(appID string) []map[string]any {
	f.t.Helper()
	resp, err := f.app.Test(httptest.NewRequest("GET", "/admin/oauth-apps/"+appID+"/users", nil), 15_000)
	if err != nil {
		f.t.Fatalf("list users: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		f.t.Fatalf("list users: %d %s", resp.StatusCode, raw)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		f.t.Fatalf("list users body: %v %s", err, raw)
	}
	return rows
}

func TestAdminListAndRevokeOneAppUser(t *testing.T) {
	f := newAdminUsersFixture(t)
	redirect := "proxima://callback"
	appID, clientID, secret := f.createApp("confidential", []string{"profile"}, []string{redirect})

	mine := f.accessTokenAs(f.app, clientID, secret, redirect)
	theirs := f.accessTokenAs(f.other, clientID, secret, redirect)
	rows := f.listUsers(appID)
	if len(rows) != 2 {
		t.Fatalf("both users must be listed, got %d", len(rows))
	}
	for _, r := range rows {
		if r["email"] == "" || r["active_tokens"] != float64(1) {
			t.Fatalf("each row needs an email and one live token: %v", r)
		}
	}

	status, body := f.do("DELETE", "/admin/oauth-apps/"+appID+"/users/"+f.otherID, "", "", nil)
	if status != 200 || body["revoked"] != float64(1) {
		t.Fatalf("revoke one: %d %v", status, body)
	}
	if s := f.bearer("/client/profile", theirs); s != 401 {
		t.Fatalf("revoked user's token must stop working, got %d", s)
	}
	if s := f.bearer("/client/profile", mine); s != 200 {
		t.Fatalf("the other user's token must keep working, got %d", s)
	}
	if rows := f.listUsers(appID); len(rows) != 1 || rows[0]["user_id"] != f.userID {
		t.Fatalf("only the remaining user may be listed: %v", rows)
	}
	if status, _ := f.do("DELETE", "/admin/oauth-apps/"+appID+"/users/"+f.otherID, "", "", nil); status != 404 {
		t.Fatalf("revoking a user who is no longer connected must be 404, got %d", status)
	}
}

func TestAdminRevokeAllAppUsersKeepsTheApp(t *testing.T) {
	f := newAdminUsersFixture(t)
	redirect := "proxima://callback"
	appID, clientID, secret := f.createApp("confidential", []string{"profile"}, []string{redirect})

	mine := f.accessTokenAs(f.app, clientID, secret, redirect)
	theirs := f.accessTokenAs(f.other, clientID, secret, redirect)

	status, body := f.do("DELETE", "/admin/oauth-apps/"+appID+"/users", "", "", nil)
	if status != 200 || body["revoked"] != float64(2) {
		t.Fatalf("revoke all: %d %v", status, body)
	}
	for name, tok := range map[string]string{"mine": mine, "theirs": theirs} {
		if s := f.bearer("/client/profile", tok); s != 401 {
			t.Fatalf("%s token must stop working after revoke-all, got %d", name, s)
		}
	}
	if rows := f.listUsers(appID); len(rows) != 0 {
		t.Fatalf("no users may remain, got %d", len(rows))
	}
	var stillThere bool
	if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM oauth_clients WHERE id = $1)`, appID).Scan(&stillThere); err != nil || !stillThere {
		t.Fatalf("the app registration must survive revoke-all: %v", err)
	}
	if s := f.bearer("/client/profile", f.accessTokenAs(f.app, clientID, secret, redirect)); s != 200 {
		t.Fatalf("approving again after revoke-all must work, got %d", s)
	}
	if status, _ := f.do("DELETE", "/admin/oauth-apps/00000000-0000-0000-0000-000000000000/users", "", "", nil); status != 404 {
		t.Fatalf("unknown app must be 404, got %d", status)
	}
	for _, path := range []string{"/admin/oauth-apps/not-a-uuid/users", "/admin/oauth-apps/" + appID + "/users/not-a-uuid"} {
		if status, _ := f.do("DELETE", path, "", "", nil); status != 404 {
			t.Fatalf("malformed id in %s must be 404, got %d", path, status)
		}
	}
	if status, _ := f.do("GET", "/admin/oauth-apps/not-a-uuid/users", "", "", nil); status != 404 {
		t.Fatalf("listing a malformed app id must be 404, got %d", status)
	}
}
