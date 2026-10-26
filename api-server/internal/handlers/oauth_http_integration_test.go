package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/middleware"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type oauthFixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	app    *fiber.App
	userID string
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed OAuth tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, sub_token, status, is_active)
		 VALUES (gen_random_uuid()::text || '@oauth.test', 'x', 'OAUTHTEST' || upper(substr(md5(random()::text), 1, 20)), 'active', true)
		 RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	h := handlers.NewOAuthHandler(pool, "https://panel.test")
	asUser := func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		c.Locals("email", "u@oauth.test")
		return c.Next()
	}
	app := fiber.New()
	app.Post("/admin/oauth-apps", h.CreateClient)
	app.Put("/admin/oauth-apps/:id", h.UpdateClient)
	app.Post("/admin/oauth-apps/:id/secret", h.RotateSecret)
	app.Get("/user/oauth/authorize", asUser, h.AuthorizeInfo)
	app.Post("/user/oauth/authorize", asUser, h.AuthorizeDecision)
	app.Get("/user/oauth/grants", asUser, h.ListGrants)
	app.Delete("/user/oauth/grants/:client_id", asUser, h.RevokeGrant)
	app.Post("/oauth/token", h.Token)
	app.Post("/oauth/revoke", h.Revoke)
	client := app.Group("/client", middleware.OAuthBearerMiddleware(h.Service()))
	client.Get("/profile", middleware.RequireScope(services.ScopeProfile), h.ClientProfile)
	client.Get("/subscription", middleware.RequireScope(services.ScopeSubscriptionRead), h.ClientSubscription)
	client.Get("/nodes", middleware.RequireScope(services.ScopeNodesRead), handlers.NewUserPlanHandler(pool).ListNodes)
	t.Cleanup(func() {
		_ = app.Shutdown()
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_clients WHERE name LIKE 'oauth-test-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return &oauthFixture{t: t, pool: pool, app: app, userID: userID}
}

func (f *oauthFixture) do(method, path, contentType, body string, header map[string]string) (int, map[string]any) {
	f.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if contentType != "" {
		req.Header.Set(fiber.HeaderContentType, contentType)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := f.app.Test(req, 15_000)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (f *oauthFixture) jsonReq(method, path string, body any) (int, map[string]any) {
	b, _ := json.Marshal(body)
	return f.do(method, path, fiber.MIMEApplicationJSON, string(b), nil)
}

func (f *oauthFixture) form(path string, v url.Values, header map[string]string) (int, map[string]any) {
	return f.do("POST", path, fiber.MIMEApplicationForm, v.Encode(), header)
}

func (f *oauthFixture) bearer(path, token string) int {
	status, _ := f.do("GET", path, "", "", map[string]string{"Authorization": "Bearer " + token})
	return status
}

func (f *oauthFixture) createApp(clientType string, scopes []string, redirects []string) (id, clientID, secret string) {
	f.t.Helper()
	status, body := f.jsonReq("POST", "/admin/oauth-apps", map[string]any{
		"name": "oauth-test-" + clientType, "client_type": clientType, "redirect_uris": redirects, "allowed_scopes": scopes,
	})
	if status != 201 {
		f.t.Fatalf("create app: %d %v", status, body)
	}
	secret, _ = body["client_secret"].(string)
	return body["id"].(string), body["client_id"].(string), secret
}

func pkcePair() (verifier, challenge string) {
	verifier = strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (f *oauthFixture) approve(clientID, redirect, scope, challenge string) string {
	f.t.Helper()
	status, body := f.jsonReq("POST", "/user/oauth/authorize", map[string]any{
		"response_type": "code", "client_id": clientID, "redirect_uri": redirect, "scope": scope, "state": "xyz",
		"code_challenge": challenge, "code_challenge_method": "S256", "approve": true,
	})
	if status != 200 {
		f.t.Fatalf("approve: %d %v", status, body)
	}
	u, err := url.Parse(body["redirect_to"].(string))
	if err != nil || u.Query().Get("state") != "xyz" || u.Query().Get("code") == "" {
		f.t.Fatalf("callback %v lacks code/state", body["redirect_to"])
	}
	return u.Query().Get("code")
}

func basic(id, secret string) map[string]string {
	return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))}
}

func TestOAuthAuthorizationCodeFlowAndScopes(t *testing.T) {
	f := newOAuthFixture(t)
	redirect := "proxima://callback"
	_, clientID, secret := f.createApp("confidential", []string{"profile", "subscription:read", "plan:read"}, []string{redirect})
	if !strings.HasPrefix(secret, "pxs_") {
		t.Fatalf("confidential app must get a one-time secret, got %q", secret)
	}
	var storedHash string
	if err := f.pool.QueryRow(context.Background(), `SELECT client_secret_hash FROM oauth_clients WHERE client_id = $1`, clientID).Scan(&storedHash); err != nil || storedHash == secret || storedHash != services.HashOAuthSecret(secret) {
		t.Fatalf("secret must be stored hashed only: %v", err)
	}

	status, info := f.do("GET", "/user/oauth/authorize?"+url.Values{"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {redirect}, "scope": {"profile subscription:read"}}.Encode(), "", "", nil)
	if status != 200 || info["client"].(map[string]any)["name"] != "oauth-test-confidential" {
		t.Fatalf("authorize info: %d %v", status, info)
	}

	verifier, challenge := pkcePair()
	code := f.approve(clientID, redirect, "profile subscription:read", challenge)
	tokenForm := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	status, tok := f.form("/oauth/token", tokenForm, basic(clientID, secret))
	if status != 200 || tok["token_type"] != "Bearer" || tok["scope"] != "profile subscription:read" {
		t.Fatalf("token exchange: %d %v", status, tok)
	}
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	if s := f.bearer("/client/profile", access); s != 200 {
		t.Fatalf("profile with granted scope: %d", s)
	}
	status, sub := f.do("GET", "/client/subscription", "", "", map[string]string{"Authorization": "Bearer " + access})
	if status != 200 || !strings.Contains(sub["url"].(string), "/sub/OAUTHTEST") {
		t.Fatalf("subscription: %d %v", status, sub)
	}
	if s := f.bearer("/client/profile", "pxat_forged"); s != 401 {
		t.Fatalf("forged token must be 401, got %d", s)
	}

	status, again := f.form("/oauth/token", tokenForm, basic(clientID, secret))
	if status != 400 || again["error"] != "invalid_grant" {
		t.Fatalf("code replay must be invalid_grant: %d %v", status, again)
	}
	if s := f.bearer("/client/profile", access); s != 401 {
		t.Fatalf("code replay must revoke the tokens it issued, got %d", s)
	}

	_ = refresh
}

func TestOAuthScopeEnforcementAndRefreshRotation(t *testing.T) {
	f := newOAuthFixture(t)
	redirect := "https://app.example.com/cb"
	_, clientID, secret := f.createApp("confidential", []string{"profile", "subscription:read"}, []string{redirect})
	verifier, challenge := pkcePair()
	code := f.approve(clientID, redirect, "profile", challenge)
	status, tok := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "client_id": {clientID}, "client_secret": {secret}}, nil)
	if status != 200 {
		t.Fatalf("token: %d %v", status, tok)
	}
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	status, body := f.do("GET", "/client/subscription", "", "", map[string]string{"Authorization": "Bearer " + access})
	if status != 403 || body["error"] != "insufficient_scope" {
		t.Fatalf("ungranted scope must be 403 insufficient_scope: %d %v", status, body)
	}

	status, wider := f.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"scope": {"profile subscription:read"}}, basic(clientID, secret))
	if status != 400 || wider["error"] != "invalid_scope" {
		t.Fatalf("refresh must not widen scope: %d %v", status, wider)
	}

	status, rotated := f.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, basic(clientID, secret))
	if status != 200 || rotated["refresh_token"] == refresh {
		t.Fatalf("refresh must rotate: %d %v", status, rotated)
	}
	newAccess := rotated["access_token"].(string)
	if s := f.bearer("/client/profile", access); s != 401 {
		t.Fatalf("rotated-out access token must be 401, got %d", s)
	}
	status, reuse := f.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, basic(clientID, secret))
	if status != 400 || reuse["error"] != "invalid_grant" {
		t.Fatalf("old refresh token must be rejected: %d %v", status, reuse)
	}
	if s := f.bearer("/client/profile", newAccess); s != 401 {
		t.Fatalf("reusing a rotated refresh token must revoke the family, got %d", s)
	}
}

func TestOAuthClientAuthenticationAndPKCE(t *testing.T) {
	f := newOAuthFixture(t)
	redirect := "http://127.0.0.1/callback"
	_, clientID, secret := f.createApp("confidential", []string{"profile"}, []string{redirect})
	verifier, challenge := pkcePair()

	code := f.approve(clientID, "http://127.0.0.1:51234/callback", "profile", challenge)
	status, body := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"http://127.0.0.1:51234/callback"}, "code_verifier": {verifier}}, basic(clientID, "pxs_wrong"))
	if status != 401 || body["error"] != "invalid_client" {
		t.Fatalf("wrong secret must be 401 invalid_client: %d %v", status, body)
	}
	status, body = f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"http://127.0.0.1:51234/callback"}, "code_verifier": {strings.Repeat("w", 50)}}, basic(clientID, secret))
	if status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("bad PKCE verifier must be 400 invalid_grant: %d %v", status, body)
	}
	status, body = f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"http://127.0.0.1:51234/other"}, "code_verifier": {verifier}}, basic(clientID, secret))
	if status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("mismatched redirect_uri at token must be invalid_grant: %d %v", status, body)
	}

	status, body = f.do("GET", "/user/oauth/authorize?"+url.Values{"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {"https://evil.example/cb"}, "scope": {"profile"}}.Encode(), "", "", nil)
	if status != 400 || body["redirect_to"] != nil {
		t.Fatalf("unregistered redirect must fail without redirect_to: %d %v", status, body)
	}
	status, body = f.do("GET", "/user/oauth/authorize?"+url.Values{"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {redirect}, "scope": {"nodes:read"}}.Encode(), "", "", nil)
	if status != 400 || body["error"] != "invalid_scope" || !strings.HasPrefix(body["redirect_to"].(string), redirect+"?") {
		t.Fatalf("scope outside the app must redirect invalid_scope: %d %v", status, body)
	}
	status, body = f.jsonReq("POST", "/user/oauth/authorize", map[string]any{"response_type": "code", "client_id": clientID,
		"redirect_uri": redirect, "scope": "profile", "state": "s1", "approve": false})
	if status != 200 || !strings.Contains(body["redirect_to"].(string), "error=access_denied") {
		t.Fatalf("deny must redirect access_denied: %d %v", status, body)
	}
}

func TestOAuthPublicClientRequiresPKCE(t *testing.T) {
	f := newOAuthFixture(t)
	redirect := "proxima://callback"
	_, clientID, secret := f.createApp("public", []string{"profile"}, []string{redirect})
	if secret != "" {
		t.Fatalf("public app must not get a secret")
	}
	status, body := f.do("GET", "/user/oauth/authorize?"+url.Values{"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {redirect}, "scope": {"profile"}}.Encode(), "", "", nil)
	if status != 400 || body["error"] != "invalid_request" {
		t.Fatalf("public client without PKCE must be rejected: %d %v", status, body)
	}
	verifier, challenge := pkcePair()
	code := f.approve(clientID, redirect, "profile", challenge)
	status, tok := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "client_id": {clientID}}, nil)
	if status != 200 {
		t.Fatalf("public PKCE exchange: %d %v", status, tok)
	}
}

func TestOAuthRevocationPaths(t *testing.T) {
	f := newOAuthFixture(t)
	redirect := "proxima://callback"
	appID, clientID, secret := f.createApp("confidential", []string{"profile"}, []string{redirect})
	issue := func() (string, string) {
		verifier, challenge := pkcePair()
		code := f.approve(clientID, redirect, "profile", challenge)
		status, tok := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
			"redirect_uri": {redirect}, "code_verifier": {verifier}}, basic(clientID, secret))
		if status != 200 {
			t.Fatalf("token: %d %v", status, tok)
		}
		return tok["access_token"].(string), tok["refresh_token"].(string)
	}
	ctx := context.Background()

	access, _ := issue()
	if s, _ := f.form("/oauth/revoke", url.Values{"token": {access}}, basic(clientID, secret)); s != 200 {
		t.Fatalf("revoke: %d", s)
	}
	if s := f.bearer("/client/profile", access); s != 401 {
		t.Fatalf("revoked token must be 401, got %d", s)
	}

	access, _ = issue()
	if s, _ := f.jsonReq("PUT", "/admin/oauth-apps/"+appID, map[string]any{"name": "oauth-test-confidential", "client_type": "confidential",
		"redirect_uris": []string{redirect}, "allowed_scopes": []string{"profile"}, "is_active": false}); s != 200 {
		t.Fatalf("disable app: %d", s)
	}
	if s := f.bearer("/client/profile", access); s != 401 {
		t.Fatalf("disabled app token must be 401, got %d", s)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE oauth_clients SET is_active = true WHERE id = $1`, appID); err != nil {
		t.Fatal(err)
	}

	access, _ = issue()
	if _, err := f.pool.Exec(ctx, `UPDATE users SET status = 'suspended' WHERE id = $1`, f.userID); err != nil {
		t.Fatal(err)
	}
	if s := f.bearer("/client/profile", access); s != 401 {
		t.Fatalf("suspended user token must be 401, got %d", s)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, f.userID); err != nil {
		t.Fatal(err)
	}

	access, refresh := issue()
	if s, _ := f.do("DELETE", "/user/oauth/grants/"+clientID, "", "", nil); s != 204 {
		t.Fatalf("disconnect app: %d", s)
	}
	if s := f.bearer("/client/profile", access); s != 401 {
		t.Fatalf("disconnected app token must be 401, got %d", s)
	}
	if s, body := f.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, basic(clientID, secret)); s != 400 {
		t.Fatalf("disconnected app refresh must fail: %d %v", s, body)
	}

	access, _ = issue()
	status, rotated := f.jsonReq("POST", "/admin/oauth-apps/"+appID+"/secret", nil)
	if status != 200 || rotated["client_secret"] == secret {
		t.Fatalf("rotate secret: %d %v", status, rotated)
	}
	if s, _ := f.form("/oauth/revoke", url.Values{"token": {access}}, basic(clientID, secret)); s != 401 {
		t.Fatalf("old secret must stop working after rotation, got %d", s)
	}
}

func TestOAuthRedirectURIValidation(t *testing.T) {
	ok := []string{"https://app.example.com/cb", "proxima://callback", "http://127.0.0.1/cb", "http://[::1]:8000/cb", "http://localhost:3000/cb", "com.example.app:/oauth"}
	bad := []string{"http://app.example.com/cb", "javascript:alert(1)", "data:text/html,x", "https://app.example.com/cb#frag", "relative/path", " https://a.example/cb", "https://user:pw@a.example/cb", "file:///etc/passwd"}
	for _, u := range ok {
		if err := services.ValidateRedirectURI(u); err != nil {
			t.Errorf("%s should be allowed: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := services.ValidateRedirectURI(u); err == nil {
			t.Errorf("%s should be rejected", u)
		}
	}
}

func TestOAuthReviewFixes(t *testing.T) {
	f := newOAuthFixture(t)
	ctx := context.Background()
	redirect := "proxima://callback"
	_, clientID, secret := f.createApp("confidential", []string{"profile", "nodes:read"}, []string{redirect})
	basicH := basic(clientID, secret)

	// Fix 5: replaying an already-used code under a DIFFERENT (valid) client
	// must not revoke the victim family.
	verifier, challenge := pkcePair()
	code := f.approve(clientID, redirect, "profile nodes:read", challenge)
	status, tok := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier}}, basicH)
	if status != 200 {
		t.Fatalf("token: %d %v", status, tok)
	}
	access := tok["access_token"].(string)
	_, otherID, otherSecret := f.createApp("confidential", []string{"profile"}, []string{redirect})
	status, body := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier}}, basic(otherID, otherSecret))
	if status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("wrong-client replay must be invalid_grant: %d %v", status, body)
	}
	if s := f.bearer("/client/profile", access); s != 200 {
		t.Fatalf("victim token must survive wrong-client replay, got %d", s)
	}
	// A replay by the code's own client still revokes.
	if s, b := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier}}, basicH); s != 400 || b["error"] != "invalid_grant" {
		t.Fatalf("own-client replay must be invalid_grant: %d %v", s, b)
	}
	if s := f.bearer("/client/profile", access); s != 401 {
		t.Fatalf("own-client replay must revoke its tokens, got %d", s)
	}

	// Fixes 2+3: mint tokens, change the password, then a NEW code must be
	// rejected at exchange and every previously issued token must die.
	verifier2, challenge2 := pkcePair()
	code2 := f.approve(clientID, redirect, "profile", challenge2)
	status, tok2 := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code2},
		"redirect_uri": {redirect}, "code_verifier": {verifier2}}, basicH)
	if status != 200 {
		t.Fatalf("token2: %d %v", status, tok2)
	}
	access2, refresh2 := tok2["access_token"].(string), tok2["refresh_token"].(string)
	// Issue the code BEFORE the password change, exchange it after.
	verifier3, challenge3 := pkcePair()
	code3 := f.approve(clientID, redirect, "profile", challenge3)
	// Pin the code's created_at into the past so the password change below is
	// unambiguously later, regardless of clock granularity.
	if _, err := f.pool.Exec(ctx,
		`UPDATE oauth_authorization_codes SET created_at = NOW() - INTERVAL '5 seconds' WHERE code_hash = $1`,
		services.HashOAuthSecret(code3)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE users SET password_hash = 'x',
		   password_changed_at = GREATEST(NOW(), (SELECT max(created_at) FROM oauth_tokens WHERE user_id = $1)) + INTERVAL '1 millisecond'
		 WHERE id = $1`, f.userID); err != nil {
		t.Fatal(err)
	}
	status, body3 := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code3},
		"redirect_uri": {redirect}, "code_verifier": {verifier3}}, basicH)
	if status != 400 || body3["error"] != "invalid_grant" {
		t.Fatalf("code issued before password change must fail: %d %v", status, body3)
	}
	if s := f.bearer("/client/profile", access2); s != 401 {
		t.Fatalf("access token must die on password change, got %d", s)
	}
	status, bodyR := f.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh2}}, basicH)
	if status != 400 {
		t.Fatalf("refresh token must die on password change: %d %v", status, bodyR)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET password_changed_at = NULL WHERE id = $1`, f.userID); err != nil {
		t.Fatal(err)
	}

	// Fix 6: re-approving with a narrower scope caps the still-live old token.
	verifier4, challenge4 := pkcePair()
	code4 := f.approve(clientID, redirect, "profile nodes:read", challenge4)
	status, tok4 := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code4},
		"redirect_uri": {redirect}, "code_verifier": {verifier4}}, basicH)
	if status != 200 {
		t.Fatalf("token4: %d %v", status, tok4)
	}
	access4 := tok4["access_token"].(string)
	if s := f.bearer("/client/nodes", access4); s != 200 {
		t.Fatalf("nodes scope before narrowing: %d", s)
	}
	verifier5, challenge5 := pkcePair()
	_ = verifier5
	f.approve(clientID, redirect, "profile", challenge5)
	if s := f.bearer("/client/nodes", access4); s != 403 {
		t.Fatalf("narrowed consent must cap the live token (403), got %d", s)
	}
	if s := f.bearer("/client/profile", access4); s != 200 {
		t.Fatalf("profile must still work after narrowing, got %d", s)
	}

	// Fix L1: loopback port-flexibility must not accept userinfo or fragment.
	_, pubID, _ := f.createApp("public", []string{"profile"}, []string{"http://127.0.0.1/cb"})
	_, pchal := pkcePair()
	status, bodyF := f.do("GET", "/user/oauth/authorize?"+url.Values{"response_type": {"code"}, "client_id": {pubID},
		"redirect_uri": {"http://user@127.0.0.1:5999/cb"}, "scope": {"profile"},
		"code_challenge": {pchal}, "code_challenge_method": {"S256"}}.Encode(), "", "", nil)
	if status != 400 || bodyF["redirect_to"] != nil {
		t.Fatalf("userinfo in loopback redirect must be rejected: %d %v", status, bodyF)
	}
	status, bodyFr := f.do("GET", "/user/oauth/authorize?"+url.Values{"response_type": {"code"}, "client_id": {pubID},
		"redirect_uri": {"http://127.0.0.1:5999/cb#frag"}, "scope": {"profile"},
		"code_challenge": {pchal}, "code_challenge_method": {"S256"}}.Encode(), "", "", nil)
	if status != 400 || bodyFr["redirect_to"] != nil {
		t.Fatalf("fragment in loopback redirect must be rejected: %d %v", status, bodyFr)
	}
}
