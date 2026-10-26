package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OAuth scopes a client app may request. Each maps to one slice of the
// account the /api/v1/client API exposes.
const (
	ScopeProfile            = "profile"
	ScopeSubscriptionRead   = "subscription:read"
	ScopeNodesRead          = "nodes:read"
	ScopePlanRead           = "plan:read"
	ScopeResetTicketsRead   = "reset_tickets:read"
	ScopeResetTicketsRedeem = "reset_tickets:redeem"
)

// OAuthScopes lists every scope in the order consent screens show them.
var OAuthScopes = []string{
	ScopeProfile, ScopeSubscriptionRead, ScopeNodesRead, ScopePlanRead,
	ScopeResetTicketsRead, ScopeResetTicketsRedeem,
}

const (
	OAuthClientConfidential = "confidential"
	OAuthClientPublic       = "public"

	oauthCodeTTL    = 10 * time.Minute
	oauthAccessTTL  = time.Hour
	oauthRefreshTTL = 30 * 24 * time.Hour

	oauthMaxRedirectURIs = 10
)

// OAuthError is an RFC 6749 error: Code is the wire "error" value.
type OAuthError struct {
	Status      int
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oauthErr(status int, code, desc string) *OAuthError {
	return &OAuthError{Status: status, Code: code, Description: desc}
}

// ErrOAuthClientNotFound is returned by admin lookups of an unknown app.
var ErrOAuthClientNotFound = errors.New("oauth client not found")

// OAuthClient is an admin-managed app. The secret itself is never stored.
type OAuthClient struct {
	ID              string     `json:"id"`
	ClientID        string     `json:"client_id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	ClientType      string     `json:"client_type"`
	RedirectURIs    []string   `json:"redirect_uris"`
	AllowedScopes   []string   `json:"allowed_scopes"`
	IsActive        bool       `json:"is_active"`
	HasSecret       bool       `json:"has_secret"`
	SecretRotatedAt *time.Time `json:"secret_rotated_at"`
	ActiveUsers     int        `json:"active_users"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	secretHash      string
}

// OAuthClientInput is the admin-editable part of an app.
type OAuthClientInput struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	ClientType    string   `json:"client_type"`
	RedirectURIs  []string `json:"redirect_uris"`
	AllowedScopes []string `json:"allowed_scopes"`
	IsActive      *bool    `json:"is_active"`
}

// OAuthTokenSet is the token endpoint's success body.
type OAuthTokenSet struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// OAuthBearer is what a valid access token resolves to.
type OAuthBearer struct {
	UserID   string
	Email    string
	Status   string
	ClientID string
	Scopes   []string
}

// OAuthAuthorizeRequest is a validated /authorize request.
type OAuthAuthorizeRequest struct {
	Client              *OAuthClient
	RedirectURI         string
	Scopes              []string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// OAuthGrantView is one connected app on the user's account page.
type OAuthGrantView struct {
	ClientID    string     `json:"client_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Scopes      []string   `json:"scopes"`
	GrantedAt   time.Time  `json:"granted_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

// OAuthService implements the authorization-code (+PKCE) and refresh flows.
type OAuthService struct {
	db *pgxpool.Pool
}

func NewOAuthService(db *pgxpool.Pool) *OAuthService { return &OAuthService{db: db} }

func randomToken(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// HashOAuthSecret is the stored form of every OAuth credential. The inputs are
// 256-bit random values, so a fast hash is enough: there is nothing to guess.
func HashOAuthSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var customSchemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.\-]*$`)

// Schemes that run code or read local data in the browser; never a redirect.
var forbiddenSchemes = map[string]bool{"javascript": true, "data": true, "file": true, "vbscript": true, "blob": true, "about": true}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateRedirectURI checks an admin-registered callback: https anywhere,
// http only on loopback (RFC 8252 native apps), or a private-use scheme such
// as proxima://callback. Fragments are never allowed (RFC 6749 3.1.2).
func ValidateRedirectURI(raw string) error {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return fmt.Errorf("redirect URI must not be empty or padded")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("redirect URI must be absolute")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("redirect URI must not contain a fragment")
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case forbiddenSchemes[scheme]:
		return fmt.Errorf("scheme %q is not allowed", scheme)
	case scheme == "https":
		if u.Host == "" {
			return fmt.Errorf("https redirect URI needs a host")
		}
	case scheme == "http":
		if !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("http redirect URIs are only allowed on loopback (127.0.0.1, ::1, localhost)")
		}
	case !customSchemePattern.MatchString(scheme):
		return fmt.Errorf("scheme %q is not valid", scheme)
	}
	if u.User != nil {
		return fmt.Errorf("redirect URI must not carry credentials")
	}
	return nil
}

// redirectMatches is exact string matching, except that a registered
// loopback http URI matches any port (RFC 8252 7.3: native apps bind an
// ephemeral port at runtime).
func redirectMatches(registered []string, requested string) bool {
	if slices.Contains(registered, requested) {
		return true
	}
	ru, err := url.Parse(requested)
	if err != nil || ru.Scheme != "http" || !isLoopbackHost(ru.Hostname()) || ru.User != nil || ru.Fragment != "" {
		return false
	}
	for _, r := range registered {
		gu, err := url.Parse(r)
		if err != nil || gu.Scheme != "http" || !isLoopbackHost(gu.Hostname()) || gu.User != nil {
			continue
		}
		if gu.Hostname() == ru.Hostname() && gu.Path == ru.Path && gu.RawQuery == ru.RawQuery {
			return true
		}
	}
	return false
}

// lockOAuthFamily serializes every exchange, refresh and consent-revocation
// for one (user, app) pair. It must be taken before any oauth row locks so all
// paths acquire locks in the same order; otherwise a replay-triggered family
// revocation could miss a concurrently rotated token.
func lockOAuthFamily(ctx context.Context, tx pgx.Tx, userID, clientID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, userID+"|"+clientID)
	return err
}

func intersectScopes(a, b []string) []string {
	out := make([]string, 0, len(a))
	for _, s := range a {
		if slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

func normalizeScopes(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !slices.Contains(OAuthScopes, s) {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(OAuthScopes, a) - slices.Index(OAuthScopes, b) })
	return out, nil
}

func validateClientInput(in *OAuthClientInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		return fmt.Errorf("name is required (max 80 characters)")
	}
	if len([]rune(in.Description)) > 500 {
		return fmt.Errorf("description is too long (max 500 characters)")
	}
	if in.ClientType != OAuthClientConfidential && in.ClientType != OAuthClientPublic {
		return fmt.Errorf("client_type must be confidential or public")
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > oauthMaxRedirectURIs {
		return fmt.Errorf("register between 1 and %d redirect URIs", oauthMaxRedirectURIs)
	}
	seen := map[string]bool{}
	for _, r := range in.RedirectURIs {
		if err := ValidateRedirectURI(r); err != nil {
			return fmt.Errorf("%s: %w", r, err)
		}
		if seen[r] {
			return fmt.Errorf("%s is listed twice", r)
		}
		seen[r] = true
	}
	scopes, err := normalizeScopes(in.AllowedScopes)
	if err != nil {
		return err
	}
	if len(scopes) == 0 {
		return fmt.Errorf("choose at least one scope")
	}
	in.AllowedScopes = scopes
	return nil
}

// ValidationError marks admin input the handler answers with 400.
type ValidationError struct{ Err error }

func (e ValidationError) Error() string { return e.Err.Error() }
func (e ValidationError) Unwrap() error { return e.Err }

const clientColumns = `c.id, c.client_id, c.name, c.description, c.client_type, c.redirect_uris,
	c.allowed_scopes, c.is_active, COALESCE(c.client_secret_hash, ''), c.secret_rotated_at,
	c.created_at, c.updated_at,
	(SELECT COUNT(*) FROM oauth_grants g WHERE g.client_id = c.id)::int`

func scanClient(row pgx.Row) (*OAuthClient, error) {
	var c OAuthClient
	if err := row.Scan(&c.ID, &c.ClientID, &c.Name, &c.Description, &c.ClientType, &c.RedirectURIs,
		&c.AllowedScopes, &c.IsActive, &c.secretHash, &c.SecretRotatedAt, &c.CreatedAt, &c.UpdatedAt, &c.ActiveUsers); err != nil {
		return nil, err
	}
	c.HasSecret = c.secretHash != ""
	return &c, nil
}

func (s *OAuthService) ListClients(ctx context.Context) ([]OAuthClient, error) {
	rows, err := s.db.Query(ctx, `SELECT `+clientColumns+` FROM oauth_clients c ORDER BY c.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OAuthClient{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *OAuthService) GetClient(ctx context.Context, id string) (*OAuthClient, error) {
	c, err := scanClient(s.db.QueryRow(ctx, `SELECT `+clientColumns+` FROM oauth_clients c WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOAuthClientNotFound
	}
	return c, err
}

func (s *OAuthService) clientByPublicID(ctx context.Context, clientID string) (*OAuthClient, error) {
	c, err := scanClient(s.db.QueryRow(ctx, `SELECT `+clientColumns+` FROM oauth_clients c WHERE c.client_id = $1`, clientID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// CreateClient registers an app and returns its secret (confidential apps
// only). The secret is shown once; only its hash is kept.
func (s *OAuthService) CreateClient(ctx context.Context, in OAuthClientInput) (*OAuthClient, string, error) {
	if err := validateClientInput(&in); err != nil {
		return nil, "", ValidationError{err}
	}
	active := in.IsActive == nil || *in.IsActive
	secret, secretHash := "", ""
	if in.ClientType == OAuthClientConfidential {
		secret = randomToken("pxs_", 32)
		secretHash = HashOAuthSecret(secret)
	}
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO oauth_clients (client_id, client_secret_hash, name, description, client_type,
		                            redirect_uris, allowed_scopes, is_active, secret_rotated_at)
		 VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, CASE WHEN $2 = '' THEN NULL ELSE NOW() END)
		 RETURNING id`,
		randomToken("pxc_", 18), secretHash, in.Name, in.Description, in.ClientType,
		in.RedirectURIs, in.AllowedScopes, active,
	).Scan(&id)
	if err != nil {
		return nil, "", err
	}
	c, err := s.GetClient(ctx, id)
	return c, secret, err
}

// UpdateClient edits an app. Narrowing allowed scopes or redirect URIs takes
// effect on the next authorization; switching to public drops the secret.
func (s *OAuthService) UpdateClient(ctx context.Context, id string, in OAuthClientInput) (*OAuthClient, string, error) {
	if err := validateClientInput(&in); err != nil {
		return nil, "", ValidationError{err}
	}
	current, err := s.GetClient(ctx, id)
	if err != nil {
		return nil, "", err
	}
	active := current.IsActive
	if in.IsActive != nil {
		active = *in.IsActive
	}
	secret := ""
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	switch {
	case in.ClientType == OAuthClientPublic:
		_, err = tx.Exec(ctx, `UPDATE oauth_clients SET client_secret_hash = NULL, secret_rotated_at = NULL WHERE id = $1`, id)
	case !current.HasSecret:
		secret = randomToken("pxs_", 32)
		_, err = tx.Exec(ctx, `UPDATE oauth_clients SET client_secret_hash = $2, secret_rotated_at = NOW() WHERE id = $1`, id, HashOAuthSecret(secret))
	}
	if err != nil {
		return nil, "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE oauth_clients SET name = $2, description = $3, client_type = $4, redirect_uris = $5,
		        allowed_scopes = $6, is_active = $7, updated_at = NOW() WHERE id = $1`,
		id, in.Name, in.Description, in.ClientType, in.RedirectURIs, in.AllowedScopes, active); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	c, err := s.GetClient(ctx, id)
	return c, secret, err
}

// RotateSecret issues a new secret; the old one stops working immediately.
func (s *OAuthService) RotateSecret(ctx context.Context, id string) (string, error) {
	c, err := s.GetClient(ctx, id)
	if err != nil {
		return "", err
	}
	if c.ClientType != OAuthClientConfidential {
		return "", ValidationError{fmt.Errorf("public apps have no client secret")}
	}
	secret := randomToken("pxs_", 32)
	if _, err := s.db.Exec(ctx,
		`UPDATE oauth_clients SET client_secret_hash = $2, secret_rotated_at = NOW(), updated_at = NOW() WHERE id = $1`,
		id, HashOAuthSecret(secret)); err != nil {
		return "", err
	}
	return secret, nil
}

func (s *OAuthService) DeleteClient(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM oauth_clients WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOAuthClientNotFound
	}
	return nil
}

// AuthorizeParams are the raw /authorize query parameters.
type AuthorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// ValidateAuthorize checks an /authorize request. A nil request with an error
// means the client or redirect URI cannot be trusted, so the caller must show
// the error and never redirect. A request plus an error is a redirectable
// error for a verified redirect URI (RFC 6749 4.1.2.1).
func (s *OAuthService) ValidateAuthorize(ctx context.Context, p AuthorizeParams) (*OAuthAuthorizeRequest, error) {
	if p.ClientID == "" {
		return nil, oauthErr(400, "invalid_request", "client_id is required")
	}
	client, err := s.clientByPublicID(ctx, p.ClientID)
	if err != nil {
		return nil, err
	}
	if client == nil || !client.IsActive {
		return nil, oauthErr(400, "invalid_client", "unknown or disabled client")
	}
	if p.RedirectURI == "" || ValidateRedirectURI(p.RedirectURI) != nil || !redirectMatches(client.RedirectURIs, p.RedirectURI) {
		return nil, oauthErr(400, "invalid_request", "redirect_uri is not registered for this client")
	}
	req := &OAuthAuthorizeRequest{Client: client, RedirectURI: p.RedirectURI, State: p.State,
		CodeChallenge: p.CodeChallenge, CodeChallengeMethod: p.CodeChallengeMethod}
	if p.ResponseType != "code" {
		return req, oauthErr(400, "unsupported_response_type", "only response_type=code is supported")
	}
	requested := client.AllowedScopes
	if strings.TrimSpace(p.Scope) != "" {
		requested, err = normalizeScopes(strings.Fields(p.Scope))
		if err != nil {
			return req, oauthErr(400, "invalid_scope", err.Error())
		}
		for _, sc := range requested {
			if !slices.Contains(client.AllowedScopes, sc) {
				return req, oauthErr(400, "invalid_scope", "scope "+sc+" is not allowed for this client")
			}
		}
	}
	if len(requested) == 0 {
		return req, oauthErr(400, "invalid_scope", "no scope requested")
	}
	req.Scopes = requested
	if p.CodeChallenge != "" || client.ClientType == OAuthClientPublic {
		if p.CodeChallengeMethod != "S256" {
			return req, oauthErr(400, "invalid_request", "PKCE with code_challenge_method=S256 is required")
		}
		if !validCodeChallenge(p.CodeChallenge) {
			return req, oauthErr(400, "invalid_request", "code_challenge must be a base64url SHA-256 digest")
		}
	}
	return req, nil
}

// validCodeChallenge reports whether s looks like an S256 PKCE challenge: the
// unpadded base64url encoding of a 32-byte SHA-256 digest is exactly 43
// characters from [A-Za-z0-9_-] (RFC 7636 4.2). Placeholders and padded or
// standard-base64 values are rejected before a code is ever issued.
// validCodeChallenge reports whether s is a PKCE code_challenge: exactly 43
// characters of the unpadded base64url alphabet (RFC 7636 section 4.2).
func validCodeChallenge(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, r := range s {
		isAlphaNum := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if !isAlphaNum && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// Approve records the user's consent and returns a single-use code.
func (s *OAuthService) Approve(ctx context.Context, userID string, req *OAuthAuthorizeRequest) (string, error) {
	code := randomToken("", 32)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockOAuthFamily(ctx, tx, userID, req.Client.ID); err != nil {
		return "", err
	}
	createdAt := time.Now()
	if _, err := tx.Exec(ctx,
		`INSERT INTO oauth_authorization_codes (code_hash, client_id, user_id, redirect_uri, scopes,
		                                        code_challenge, code_challenge_method, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		HashOAuthSecret(code), req.Client.ID, userID, req.RedirectURI, req.Scopes,
		req.CodeChallenge, req.CodeChallengeMethod, createdAt.Add(oauthCodeTTL), createdAt); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO oauth_grants (user_id, client_id, scopes) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, client_id) DO UPDATE SET scopes = EXCLUDED.scopes, updated_at = NOW()`,
		userID, req.Client.ID, req.Scopes); err != nil {
		return "", err
	}
	_, _ = tx.Exec(ctx, `DELETE FROM oauth_authorization_codes WHERE expires_at < NOW() - INTERVAL '1 day'`)
	return code, tx.Commit(ctx)
}

// AuthenticateClient checks token-endpoint client credentials. Confidential
// apps must present their secret; public apps present none and rely on PKCE.
func (s *OAuthService) AuthenticateClient(ctx context.Context, clientID, secret string) (*OAuthClient, error) {
	invalid := oauthErr(401, "invalid_client", "client authentication failed")
	if clientID == "" {
		return nil, invalid
	}
	c, err := s.clientByPublicID(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if c == nil || !c.IsActive {
		return nil, invalid
	}
	if c.ClientType == OAuthClientConfidential {
		if secret == "" || c.secretHash == "" ||
			subtle.ConstantTimeCompare([]byte(HashOAuthSecret(secret)), []byte(c.secretHash)) != 1 {
			return nil, invalid
		}
	}
	return c, nil
}

func pkceMatches(challenge, verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func (s *OAuthService) issueTokens(ctx context.Context, tx pgx.Tx, clientID, userID string, scopes []string, codeHash string) (*OAuthTokenSet, error) {
	access := randomToken("pxat_", 32)
	refresh := randomToken("pxrt_", 32)
	now := time.Now()
	if _, err := tx.Exec(ctx,
		`INSERT INTO oauth_tokens (client_id, user_id, access_token_hash, refresh_token_hash, scopes,
		                           code_hash, access_expires_at, refresh_expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		clientID, userID, HashOAuthSecret(access), HashOAuthSecret(refresh), scopes, codeHash,
		now.Add(oauthAccessTTL), now.Add(oauthRefreshTTL), now); err != nil {
		return nil, err
	}
	return &OAuthTokenSet{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(oauthAccessTTL.Seconds()),
		RefreshToken: refresh, Scope: strings.Join(scopes, " ")}, nil
}

// revokeFamilyLocked ends every token minted from one authorization code.
// The caller must hold the (user, client) family lock so a concurrent Refresh
// cannot commit a new token after this UPDATE's snapshot.
func revokeFamilyLocked(ctx context.Context, tx pgx.Tx, codeHash string) error {
	if codeHash == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE oauth_tokens SET revoked_at = NOW() WHERE code_hash = $1 AND revoked_at IS NULL`, codeHash)
	return err
}

// ExchangeCode redeems an authorization code. A code used twice revokes every
// token it already produced (RFC 6749 4.1.2).
func (s *OAuthService) ExchangeCode(ctx context.Context, client *OAuthClient, code, redirectURI, verifier string) (*OAuthTokenSet, error) {
	badGrant := oauthErr(400, "invalid_grant", "authorization code is invalid, expired or already used")
	if code == "" {
		return nil, oauthErr(400, "invalid_request", "code is required")
	}
	codeHash := HashOAuthSecret(code)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var (
		codeClient, userID, storedRedirect, challenge, method string
		scopes                                                []string
		expiresAt, createdAt                                  time.Time
		usedAt                                                *time.Time
		passwordChangedAt                                     *time.Time
		userActive                                            bool
	)
	err = tx.QueryRow(ctx,
		`SELECT a.client_id, a.user_id, a.redirect_uri, a.scopes, a.code_challenge, a.code_challenge_method,
		        a.expires_at, a.used_at, a.created_at, u.password_changed_at,
		        (u.is_active AND u.status <> 'suspended')
		 FROM oauth_authorization_codes a JOIN users u ON u.id = a.user_id
		 WHERE a.code_hash = $1 FOR UPDATE OF a`, codeHash,
	).Scan(&codeClient, &userID, &storedRedirect, &scopes, &challenge, &method, &expiresAt, &usedAt, &createdAt,
		&passwordChangedAt, &userActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badGrant
	}
	if err != nil {
		return nil, err
	}
	if err := lockOAuthFamily(ctx, tx, userID, codeClient); err != nil {
		return nil, err
	}
	if codeClient != client.ID {
		// Never let a third party's client binding trigger the replay
		// revocation below on someone else's code.
		return nil, badGrant
	}
	if usedAt != nil {
		if err := revokeFamilyLocked(ctx, tx, codeHash); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, badGrant
	}
	if time.Now().After(expiresAt) || storedRedirect != redirectURI || !userActive ||
		(passwordChangedAt != nil && !createdAt.After(*passwordChangedAt)) {
		return nil, badGrant
	}
	if challenge != "" {
		if method != "S256" || !pkceMatches(challenge, verifier) {
			return nil, oauthErr(400, "invalid_grant", "PKCE verification failed")
		}
	} else if client.ClientType == OAuthClientPublic {
		return nil, badGrant
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_authorization_codes SET used_at = NOW() WHERE code_hash = $1`, codeHash); err != nil {
		return nil, err
	}
	var granted []string
	if err := tx.QueryRow(ctx,
		`SELECT scopes FROM oauth_grants WHERE user_id = $1 AND client_id = $2`, userID, client.ID,
	).Scan(&granted); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if len(granted) > 0 {
		scopes = intersectScopes(scopes, granted)
	}
	if len(scopes) == 0 {
		return nil, badGrant
	}
	set, err := s.issueTokens(ctx, tx, client.ID, userID, scopes, codeHash)
	if err != nil {
		return nil, err
	}
	return set, tx.Commit(ctx)
}

// Refresh rotates a refresh token. Presenting a refresh token that was
// already rotated or revoked revokes the whole token family, since it means
// two parties hold it.
func (s *OAuthService) Refresh(ctx context.Context, client *OAuthClient, refresh, scope string) (*OAuthTokenSet, error) {
	badGrant := oauthErr(400, "invalid_grant", "refresh token is invalid, expired or revoked")
	if refresh == "" {
		return nil, oauthErr(400, "invalid_request", "refresh_token is required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var (
		tokenID, tokenClient, userID, codeHash string
		scopes, granted                        []string
		refreshExpires                         *time.Time
		revokedAt                              *time.Time
		createdAt                              time.Time
		passwordChangedAt                      *time.Time
		userActive, hasGrant                   bool
	)
	err = tx.QueryRow(ctx,
		`SELECT t.id, t.client_id, t.user_id, t.scopes, t.code_hash, t.refresh_expires_at, t.revoked_at, t.created_at,
		        u.password_changed_at, (u.is_active AND u.status <> 'suspended'),
		        g.user_id IS NOT NULL, COALESCE(g.scopes, '{}')
		 FROM oauth_tokens t
		 JOIN users u ON u.id = t.user_id
		 LEFT JOIN oauth_grants g ON g.user_id = t.user_id AND g.client_id = t.client_id
		 WHERE t.refresh_token_hash = $1 FOR UPDATE OF t`, HashOAuthSecret(refresh),
	).Scan(&tokenID, &tokenClient, &userID, &scopes, &codeHash, &refreshExpires, &revokedAt, &createdAt,
		&passwordChangedAt, &userActive, &hasGrant, &granted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badGrant
	}
	if err != nil {
		return nil, err
	}
	if err := lockOAuthFamily(ctx, tx, userID, tokenClient); err != nil {
		return nil, err
	}
	if tokenClient != client.ID {
		return nil, badGrant
	}
	if revokedAt != nil {
		if err := revokeFamilyLocked(ctx, tx, codeHash); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, badGrant
	}
	if refreshExpires == nil || time.Now().After(*refreshExpires) || !userActive || !hasGrant ||
		(passwordChangedAt != nil && !createdAt.After(*passwordChangedAt)) {
		return nil, badGrant
	}
	next := scopes
	if strings.TrimSpace(scope) != "" {
		next, err = normalizeScopes(strings.Fields(scope))
		if err != nil {
			return nil, oauthErr(400, "invalid_scope", err.Error())
		}
		for _, sc := range next {
			if !slices.Contains(scopes, sc) {
				return nil, oauthErr(400, "invalid_scope", "refresh cannot widen scope")
			}
		}
	}
	next = slices.DeleteFunc(slices.Clone(next), func(sc string) bool {
		return !slices.Contains(granted, sc) || !slices.Contains(client.AllowedScopes, sc)
	})
	if len(next) == 0 {
		return nil, badGrant
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_tokens SET revoked_at = NOW() WHERE id = $1`, tokenID); err != nil {
		return nil, err
	}
	set, err := s.issueTokens(ctx, tx, client.ID, userID, next, codeHash)
	if err != nil {
		return nil, err
	}
	return set, tx.Commit(ctx)
}

// RevokeToken ends the access or refresh token a client presents (RFC 7009).
// Unknown tokens succeed silently so the endpoint cannot probe for tokens.
func (s *OAuthService) RevokeToken(ctx context.Context, client *OAuthClient, token string) error {
	if token == "" {
		return nil
	}
	h := HashOAuthSecret(token)
	_, err := s.db.Exec(ctx,
		`UPDATE oauth_tokens SET revoked_at = NOW()
		 WHERE client_id = $1 AND revoked_at IS NULL AND (access_token_hash = $2 OR refresh_token_hash = $2)`,
		client.ID, h)
	return err
}

// AuthenticateBearer resolves an access token for the /client API. A token is
// dead once revoked, expired, its app is disabled, the user withdrew consent,
// the user is suspended, or the password changed after it was issued. The
// returned scopes are capped by the latest consent, so re-authorizing with a
// narrower scope takes effect on live tokens too.
func (s *OAuthService) AuthenticateBearer(ctx context.Context, token string) (*OAuthBearer, error) {
	var (
		b       OAuthBearer
		tokID   string
		granted []string
	)
	err := s.db.QueryRow(ctx,
		`SELECT t.id, t.user_id, u.email, u.status, c.client_id, t.scopes, g.scopes
		 FROM oauth_tokens t
		 JOIN oauth_clients c ON c.id = t.client_id
		 JOIN users u ON u.id = t.user_id
		 JOIN oauth_grants g ON g.user_id = t.user_id AND g.client_id = t.client_id
		 WHERE t.access_token_hash = $1
		   AND t.revoked_at IS NULL AND t.access_expires_at > NOW()
		   AND c.is_active AND u.is_active AND u.status <> 'suspended'
		   AND (u.password_changed_at IS NULL OR t.created_at > u.password_changed_at)`,
		HashOAuthSecret(token),
	).Scan(&tokID, &b.UserID, &b.Email, &b.Status, &b.ClientID, &b.Scopes, &granted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, oauthErr(401, "invalid_token", "access token is invalid, expired or revoked")
	}
	if err != nil {
		return nil, err
	}
	b.Scopes = intersectScopes(b.Scopes, granted)
	_, _ = s.db.Exec(ctx, `UPDATE oauth_tokens SET last_used_at = NOW() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`, tokID)
	return &b, nil
}

// ListGrants returns the apps a user has authorized.
func (s *OAuthService) ListGrants(ctx context.Context, userID string) ([]OAuthGrantView, error) {
	rows, err := s.db.Query(ctx,
		`SELECT c.client_id, c.name, c.description, g.scopes, g.created_at,
		        (SELECT MAX(t.last_used_at) FROM oauth_tokens t WHERE t.user_id = g.user_id AND t.client_id = g.client_id)
		 FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id
		 WHERE g.user_id = $1 ORDER BY g.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OAuthGrantView{}
	for rows.Next() {
		var v OAuthGrantView
		if err := rows.Scan(&v.ClientID, &v.Name, &v.Description, &v.Scopes, &v.GrantedAt, &v.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevokeGrant withdraws a user's consent for one app and ends its tokens. The
// (user, client) family lock keeps a concurrent refresh from minting a token
// that this revocation would otherwise miss.
func (s *OAuthService) RevokeGrant(ctx context.Context, userID, clientID string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var internalID string
	err = tx.QueryRow(ctx,
		`SELECT c.id FROM oauth_clients c WHERE c.client_id = $1 FOR UPDATE OF c`, clientID,
	).Scan(&internalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := lockOAuthFamily(ctx, tx, userID, internalID); err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx,
		`DELETE FROM oauth_grants WHERE user_id = $1 AND client_id = $2 RETURNING client_id`, userID, internalID,
	).Scan(&internalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE oauth_tokens SET revoked_at = NOW() WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL`,
		userID, internalID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE oauth_authorization_codes SET used_at = NOW() WHERE user_id = $1 AND client_id = $2 AND used_at IS NULL`,
		userID, internalID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// OAuthAppUser is one user who has authorized an app, as the admin sees it.
type OAuthAppUser struct {
	UserID       string     `json:"user_id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Scopes       []string   `json:"scopes"`
	GrantedAt    time.Time  `json:"granted_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	ActiveTokens int        `json:"active_tokens"`
}

// ListAppUsers returns every user who currently authorizes the app with the
// given internal ID, most recently used first.
func (s *OAuthService) ListAppUsers(ctx context.Context, appID string) ([]OAuthAppUser, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM oauth_clients WHERE id = $1)`, appID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrOAuthClientNotFound
	}
	rows, err := s.db.Query(ctx,
		`SELECT u.id::text, u.email, COALESCE(u.name, ''), g.scopes, g.created_at,
		        (SELECT MAX(t.last_used_at) FROM oauth_tokens t WHERE t.user_id = g.user_id AND t.client_id = g.client_id),
		        (SELECT COUNT(*) FROM oauth_tokens t WHERE t.user_id = g.user_id AND t.client_id = g.client_id
		           AND t.revoked_at IS NULL AND (t.access_expires_at > NOW() OR t.refresh_expires_at > NOW()))
		 FROM oauth_grants g JOIN users u ON u.id = g.user_id
		 WHERE g.client_id = $1
		 ORDER BY 6 DESC NULLS LAST, g.created_at DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OAuthAppUser{}
	for rows.Next() {
		var v OAuthAppUser
		if err := rows.Scan(&v.UserID, &v.Email, &v.Name, &v.Scopes, &v.GrantedAt, &v.LastUsedAt, &v.ActiveTokens); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevokeAppUsers withdraws consent for the app with the given internal ID and
// ends its tokens and pending codes. With userID set only that user is
// disconnected; empty userID disconnects everyone. The app row is locked first
// and every (user, app) family lock is taken in user order, matching
// RevokeGrant, so a concurrent refresh cannot mint a token this misses.
func (s *OAuthService) RevokeAppUsers(ctx context.Context, appID, userID string) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	err = tx.QueryRow(ctx, `SELECT id FROM oauth_clients WHERE id = $1 FOR UPDATE`, appID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrOAuthClientNotFound
	}
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx,
		`SELECT user_id::text FROM oauth_grants WHERE client_id = $1 AND ($2 = '' OR user_id::text = $2) ORDER BY user_id`,
		appID, userID)
	if err != nil {
		return 0, err
	}
	var users []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		users = append(users, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range users {
		if err := lockOAuthFamily(ctx, tx, id, appID); err != nil {
			return 0, err
		}
	}
	if len(users) == 0 {
		return 0, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM oauth_grants WHERE client_id = $1 AND user_id::text = ANY($2)`, appID, users); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE oauth_tokens SET revoked_at = NOW() WHERE client_id = $1 AND user_id::text = ANY($2) AND revoked_at IS NULL`,
		appID, users); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE oauth_authorization_codes SET used_at = NOW() WHERE client_id = $1 AND user_id::text = ANY($2) AND used_at IS NULL`,
		appID, users); err != nil {
		return 0, err
	}
	return len(users), tx.Commit(ctx)
}
