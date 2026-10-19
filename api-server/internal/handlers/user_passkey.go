package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

const (
	passkeyLoginSessionTTL = 3 * time.Minute
	passkeyRegSessionTTL   = 3 * time.Minute
	maxPasskeysPerUser     = 10
)

type passkeyUser struct {
	id          string
	email       string
	name        string
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte   { b, _ := uuid.Parse(u.id); return b[:] }
func (u *passkeyUser) WebAuthnName() string { return u.email }
func (u *passkeyUser) WebAuthnDisplayName() string {
	if strings.TrimSpace(u.name) != "" {
		return u.name
	}
	return u.email
}
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func (h *UserAuthHandler) passkeyReady(c *fiber.Ctx) bool {
	if h.webauthn == nil || h.redis == nil || h.db == nil {
		_ = c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "passkey authentication is unavailable"})
		return false
	}
	return true
}

func (h *UserAuthHandler) putPasskeySession(ctx context.Context, kind string, s *webauthn.SessionData, userID string) (string, error) {
	id := uuid.NewString()
	payload, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	ttl := passkeyLoginSessionTTL
	if kind == "register" {
		ttl = passkeyRegSessionTTL
	}
	key := "webauthn:" + kind + ":" + id
	if userID != "" {
		key += ":" + userID
	}
	if err := h.redis.Set(ctx, key, payload, ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (h *UserAuthHandler) takePasskeySession(ctx context.Context, kind, id, userID string) (*webauthn.SessionData, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errors.New("invalid ceremony session")
	}
	key := "webauthn:" + kind + ":" + id
	if userID != "" {
		key += ":" + userID
	}
	payload, err := h.redis.GetDel(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	var s webauthn.SessionData
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (h *UserAuthHandler) loadPasskeyUser(ctx context.Context, userID string) (*passkeyUser, error) {
	var u passkeyUser
	if err := h.db.QueryRow(ctx, `SELECT id::text, email, name FROM users WHERE id=$1 AND is_active=true`, userID).Scan(&u.id, &u.email, &u.name); err != nil {
		return nil, err
	}
	rows, err := h.db.Query(ctx, `SELECT credential FROM user_passkeys WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var cred webauthn.Credential
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &cred); err != nil {
			return nil, err
		}
		u.credentials = append(u.credentials, cred)
	}
	return &u, rows.Err()
}

// ListPasskeys lists passkeys belonging to the authenticated user.
func (h *UserAuthHandler) ListPasskeys(c *fiber.Ctx) error {
	uid := c.Locals("user_id").(string)
	rows, err := h.db.Query(c.UserContext(), `SELECT id::text,name,created_at,last_used_at FROM user_passkeys WHERE user_id=$1 ORDER BY created_at`, uid)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	defer rows.Close()
	items := make([]fiber.Map, 0)
	for rows.Next() {
		var id, name string
		var created time.Time
		var last *time.Time
		if err := rows.Scan(&id, &name, &created, &last); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		items = append(items, fiber.Map{"id": id, "name": name, "created_at": created, "last_used_at": last})
	}
	if err := rows.Err(); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	return c.JSON(items)
}

// BeginPasskeyRegistration re-authenticates the user with their password before issuing registration options.
func (h *UserAuthHandler) BeginPasskeyRegistration(c *fiber.Ctx) error {
	if !h.passkeyReady(c) {
		return nil
	}
	uid := c.Locals("user_id").(string)
	var req struct {
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "password is required"})
	}
	var hash string
	if err := h.db.QueryRow(c.UserContext(), `SELECT password_hash FROM users WHERE id=$1 AND is_active=true AND status <> 'suspended'`, uid).Scan(&hash); err != nil {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	if !crypto.CheckPassword(hash, req.Password) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "incorrect password"})
	}
	var count int
	if err := h.db.QueryRow(c.UserContext(), `SELECT count(*) FROM user_passkeys WHERE user_id=$1`, uid).Scan(&count); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if count >= maxPasskeysPerUser {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "passkey limit reached"})
	}
	u, err := h.loadPasskeyUser(c.UserContext(), uid)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	creation, session, err := h.webauthn.BeginRegistration(u,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(u.credentials).CredentialDescriptors()),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "could not start passkey registration"})
	}
	sid, err := h.putPasskeySession(c.UserContext(), "register", session, uid)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	c.Cookie(&fiber.Cookie{Name: "passkey_registration_session", Value: sid, Path: "/api/v1/user/passkeys/register", HTTPOnly: true, Secure: h.passkeySecure, SameSite: "Strict", MaxAge: int(passkeyRegSessionTTL.Seconds())})
	return c.JSON(fiber.Map{"options": creation.Response})
}

func (h *UserAuthHandler) FinishPasskeyRegistration(c *fiber.Ctx) error {
	if !h.passkeyReady(c) {
		return nil
	}
	uid := c.Locals("user_id").(string)
	var req struct {
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.Credential) == 0 || len(req.Credential) > 64*1024 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid credential response"})
	}
	sid := c.Cookies("passkey_registration_session")
	session, err := h.takePasskeySession(c.UserContext(), "register", sid, uid)
	c.Cookie(&fiber.Cookie{Name: "passkey_registration_session", Value: "", Path: "/api/v1/user/passkeys/register", HTTPOnly: true, Secure: h.passkeySecure, SameSite: "Strict", MaxAge: -1})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "registration expired; try again"})
	}
	u, err := h.loadPasskeyUser(c.UserContext(), uid)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(string(req.Credential)))
	r.Header.Set("Content-Type", "application/json")
	cred, err := h.webauthn.FinishRegistration(u, *session, r)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "passkey verification failed"})
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 64 {
		name = services.TruncateUTF8(name, 64)
	}
	if name == "" {
		name = "Passkey"
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	var id string
	err = h.db.QueryRow(c.UserContext(), `INSERT INTO user_passkeys(user_id,credential_id,credential,name) VALUES($1,$2,$3,$4) RETURNING id::text`, uid, cred.ID, string(raw), name).Scan(&id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "this passkey is already registered"})
		}
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	var created time.Time
	_ = h.db.QueryRow(c.UserContext(), `SELECT created_at FROM user_passkeys WHERE id=$1`, id).Scan(&created)
	h.activity.Log(c.UserContext(), services.Record{EventType: "user.passkey_added", Severity: services.SeverityInfo, ActorType: "user", ActorID: uid, ActorLabel: u.email, Detail: map[string]any{"name": name}})
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": name, "created_at": created, "last_used_at": nil})
}

func (h *UserAuthHandler) DeletePasskey(c *fiber.Ctx) error {
	uid := c.Locals("user_id").(string)
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return c.SendStatus(fiber.StatusNotFound)
	}
	var name string
	err := h.db.QueryRow(c.UserContext(), `DELETE FROM user_passkeys WHERE id=$1 AND user_id=$2 RETURNING name`, id, uid).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.SendStatus(fiber.StatusNotFound)
	}
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	var email string
	_ = h.db.QueryRow(c.UserContext(), `SELECT email FROM users WHERE id=$1`, uid).Scan(&email)
	h.activity.Log(c.UserContext(), services.Record{EventType: "user.passkey_removed", Severity: services.SeverityInfo, ActorType: "user", ActorID: uid, ActorLabel: email, Detail: map[string]any{"name": name}})
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *UserAuthHandler) BeginPasskeyLogin(c *fiber.Ctx) error {
	if !h.passkeyReady(c) {
		return nil
	}
	assertion, session, err := h.webauthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "could not start passkey login"})
	}
	sid, err := h.putPasskeySession(c.UserContext(), "login", session, "")
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	c.Cookie(&fiber.Cookie{Name: "passkey_login_session", Value: sid, Path: "/api/v1/auth/passkey/login", HTTPOnly: true, Secure: h.passkeySecure, SameSite: "Strict", MaxAge: int(passkeyLoginSessionTTL.Seconds())})
	return c.JSON(fiber.Map{"session_id": sid, "options": assertion.Response})
}

func (h *UserAuthHandler) FinishPasskeyLogin(c *fiber.Ctx) error {
	if !h.passkeyReady(c) {
		return nil
	}
	var req struct {
		SessionID  string          `json:"session_id"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.Credential) == 0 || len(req.Credential) > 64*1024 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "passkey authentication failed"})
	}
	if req.SessionID == "" || req.SessionID != c.Cookies("passkey_login_session") {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "passkey authentication failed"})
	}
	c.Cookie(&fiber.Cookie{Name: "passkey_login_session", Value: "", Path: "/api/v1/auth/passkey/login", HTTPOnly: true, Secure: h.passkeySecure, SameSite: "Strict", MaxAge: -1})
	session, err := h.takePasskeySession(c.UserContext(), "login", req.SessionID, "")
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "passkey authentication failed"})
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "passkey authentication failed"})
	}
	lookup := func(rawID, userHandle []byte) (webauthn.User, error) {
		var uid string
		parsedID, e := uuid.FromBytes(userHandle)
		if e != nil {
			return nil, errors.New("invalid user handle")
		}
		if e := h.db.QueryRow(c.UserContext(), `SELECT id::text FROM users WHERE id=$1 AND is_active=true`, parsedID.String()).Scan(&uid); e != nil {
			return nil, e
		}
		u, e := h.loadPasskeyUser(c.UserContext(), uid)
		if e != nil {
			return nil, e
		}
		for _, cr := range u.credentials {
			if bytes.Equal(cr.ID, rawID) {
				return u, nil
			}
		}
		return nil, errors.New("credential not found")
	}
	u, credential, err := h.webauthn.ValidatePasskeyLogin(lookup, *session, parsed)
	if err != nil {
		h.recordLogin(c, "", "", false, services.LoginFailureBadPasskey)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "passkey authentication failed"})
	}
	pu, ok := u.(*passkeyUser)
	if !ok {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	id := pu.id
	// A repeated non-zero signature counter marks a cloned authenticator; do
	// not mint a session from a credential the library has flagged.
	if credential.Authenticator.CloneWarning {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "passkey authentication failed"})
	}
	var status string
	var isActive bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT status,is_active FROM users WHERE id=$1`, id).Scan(&status, &isActive); err != nil || !isActive {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "passkey authentication failed"})
	}
	if status == "suspended" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "account suspended"})
	}
	// Persist updated authenticator counters/backup flags and use time.
	credJSON, err := json.Marshal(credential)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if _, err = h.db.Exec(c.UserContext(), `UPDATE user_passkeys SET credential=$1,last_used_at=NOW() WHERE user_id=$2 AND credential_id=$3`, string(credJSON), id, credential.ID); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	now := time.Now()
	expiry := resolveSessionExpiry(c.UserContext(), h.db, h.jwtExpiry)
	claims := UserClaims{UserID: id, Email: pu.email, Status: status, RegisteredClaims: jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(expiry))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(h.jwtSecret))
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	h.activity.Log(c.UserContext(), services.Record{EventType: services.EventUserLogin, Severity: services.SeverityInfo, ActorType: "user", ActorID: id, ActorLabel: pu.email, Detail: map[string]any{"ip": c.IP(), "method": "passkey"}})
	h.recordLogin(c, id, pu.email, true, "")
	return c.JSON(fiber.Map{"token": token})
}

// Compile-time check that the Redis API used for one-time ceremony sessions remains available.
