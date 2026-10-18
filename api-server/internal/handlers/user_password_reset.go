package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/lang"
)

const (
	// passwordResetTokenTTL is how long an emailed link works.
	passwordResetTokenTTL = 30 * time.Minute
	// passwordResetCooldown is the minimum gap between two tokens for one
	// account, so the endpoint cannot be used to flood a victim's inbox.
	passwordResetCooldown = 60 * time.Second
	// passwordResetWorkTimeout bounds one background request: the token insert
	// plus the whole SMTP session.
	passwordResetWorkTimeout = 45 * time.Second
	// passwordResetMaxInFlight bounds concurrent background requests. A request
	// arriving while all slots are busy is dropped (and logged); the HTTP
	// response is the same either way.
	passwordResetMaxInFlight = 32

	// minPasswordLength applies to reset only: registration and profile
	// updates enforce no minimum at all.
	minPasswordLength = 8
	// maxPasswordBytes is bcrypt's input limit; a longer password would fail to
	// hash and surface as a 500.
	maxPasswordBytes = 72

	// maxResetTokenLength rejects absurd input before hashing it. Real tokens are
	// 43 characters.
	maxResetTokenLength = 128

	forgotPasswordMessage = "If an account exists for that email, a password reset link has been sent."
	resetPasswordMessage  = "Your password has been reset. You can now sign in with your new password."
	invalidResetToken     = "invalid or expired reset token"
)

type forgotPasswordRequest struct {
	Email    string `json:"email"`
	Language string `json:"language"`
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// ForgotPassword starts a password reset by emailing a single-use link.
// @Summary Request a password reset email
// @Description Always answers 200 for a well-formed request, whether or not the account exists, so it cannot be used to discover accounts.
// @Tags user-auth
// @Accept json
// @Produce json
// @Param body body forgotPasswordRequest true "Account email and email language (ko, en or zh)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 429 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /auth/forgot-password [post]
func (h *UserAuthHandler) ForgotPassword(c *fiber.Ctx) error {
	var req forgotPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	email := strings.TrimSpace(req.Email)
	if email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email is required"})
	}
	if len(email) > 254 || !emailRegex.MatchString(email) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email format"})
	}

	// Decided from configuration alone, never from the address, so it reveals
	// nothing about which accounts exist.
	if h.mailer == nil || h.resetBaseURL == "" {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "password reset email is not configured",
		})
	}

	// Everything past this point runs after the response: the lookup, the
	// token insert and the SMTP session would otherwise make a known address
	// measurably slower than an unknown one. c.IP() can alias the request
	// buffer, so it is copied before the context is recycled.
	h.queuePasswordReset(email, normalizeResetLanguage(req.Language), strings.Clone(c.IP()))

	return c.JSON(fiber.Map{"message": forgotPasswordMessage})
}

// ResetPassword completes a reset: it consumes the emailed token and sets the
// new password.
// @Summary Reset a password with an emailed token
// @Tags user-auth
// @Accept json
// @Produce json
// @Param body body resetPasswordRequest true "Reset token and new password"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 429 {object} map[string]string
// @Router /auth/reset-password [post]
func (h *UserAuthHandler) ResetPassword(c *fiber.Ctx) error {
	var req resetPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	token := strings.TrimSpace(req.Token)
	if token == "" || len(token) > maxResetTokenLength {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": invalidResetToken})
	}
	// Checked before the token so a weak password never costs the user their
	// link, and it says nothing about the token either way.
	if err := validateNewPassword(req.Password); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	ctx := c.UserContext()
	tokenHash := hashResetToken(token)

	// Cheap read first: an unknown token must not cost a bcrypt hash.
	usable, err := h.resetTokenUsable(ctx, tokenHash)
	if err != nil {
		log.Printf("[PasswordReset] checking token: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if !usable {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": invalidResetToken})
	}

	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	userID, email, ok, err := h.consumeResetToken(ctx, tokenHash, passwordHash)
	if err != nil {
		log.Printf("[PasswordReset] applying reset: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if !ok {
		// Lost a race with another use of the same token, or it expired
		// between the check and the lock.
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": invalidResetToken})
	}

	h.activity.Log(ctx, services.Record{
		EventType:  services.EventUserPasswordReset,
		Severity:   services.SeverityInfo,
		ActorType:  "user",
		ActorID:    userID,
		ActorLabel: email,
		TargetType: "user",
		TargetID:   userID,
		Detail:     map[string]any{"ip": c.IP()},
	})

	return c.JSON(fiber.Map{"message": resetPasswordMessage})
}

// queuePasswordReset runs the account-dependent half of ForgotPassword in the
// background. Panics are contained because a goroutine outside a request is not
// covered by the recover middleware.
func (h *UserAuthHandler) queuePasswordReset(email string, language lang.Code, ip string) {
	select {
	case h.resetSlots <- struct{}{}:
	default:
		log.Printf("[PasswordReset] %d requests already in flight; dropping one", passwordResetMaxInFlight)
		return
	}

	h.resetWG.Add(1)
	go func() {
		defer h.resetWG.Done()
		defer func() { <-h.resetSlots }()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PasswordReset] recovered from panic: %v", r)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), passwordResetWorkTimeout)
		defer cancel()
		h.processPasswordReset(ctx, email, language, ip)
	}()
}

// processPasswordReset creates a token for the account, if there is an eligible
// one, and emails the link. Neither the token nor the link is ever logged.
func (h *UserAuthHandler) processPasswordReset(ctx context.Context, email string, language lang.Code, ip string) {
	userID, token, err := h.issueResetToken(ctx, email, ip)
	if err != nil {
		log.Printf("[PasswordReset] creating token: %v", err)
		return
	}
	if token == "" {
		return // unknown address, suspended account, or inside the cooldown
	}

	link := h.resetBaseURL + "/reset-password?token=" + url.QueryEscape(token)
	subject, body := mail.PasswordResetEmail(language, link, passwordResetTokenTTL)
	if err := h.mailer.Send(ctx, mail.Message{To: email, Subject: subject, Body: body}); err != nil {
		log.Printf("[PasswordReset] sending email for user %s failed: %v", userID, err)
	}
}

// issueResetToken stores a fresh token for the account and returns the raw
// value for the email. It returns an empty token when nothing should be sent.
//
// The user row is locked for the duration so two simultaneous requests cannot
// both pass the cooldown check.
func (h *UserAuthHandler) issueResetToken(ctx context.Context, email, ip string) (userID, token string, err error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return "", "", fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status   string
		isActive bool
	)
	err = tx.QueryRow(ctx,
		`SELECT id::text, status, is_active FROM users WHERE email = $1 FOR UPDATE`, email,
	).Scan(&userID, &status, &isActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("looking up user: %w", err)
	}
	if !isActive || status == "suspended" {
		return "", "", nil
	}

	var cooling bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM password_reset_tokens
			WHERE user_id = $1 AND created_at > NOW() - make_interval(secs => $2))`,
		userID, passwordResetCooldown.Seconds(),
	).Scan(&cooling); err != nil {
		return "", "", fmt.Errorf("checking cooldown: %w", err)
	}
	if cooling {
		return "", "", nil
	}

	raw, hash, err := newResetToken()
	if err != nil {
		return "", "", err
	}
	if len(ip) > 64 {
		ip = ip[:64]
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at, request_ip)
		 VALUES ($1, $2, NOW() + make_interval(secs => $3), $4)`,
		userID, hash, passwordResetTokenTTL.Seconds(), ip,
	); err != nil {
		return "", "", fmt.Errorf("storing token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("commit: %w", err)
	}
	return userID, raw, nil
}

// resetTokenUsable reports whether the hash names an unused, unexpired token of
// an account that may still sign in.
func (h *UserAuthHandler) resetTokenUsable(ctx context.Context, tokenHash string) (bool, error) {
	var usable bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM password_reset_tokens t
			JOIN users u ON u.id = t.user_id
			WHERE t.token_hash = $1 AND t.used_at IS NULL AND t.expires_at > NOW()
			  AND u.is_active AND u.status <> 'suspended')`,
		tokenHash,
	).Scan(&usable)
	return usable, err
}

// consumeResetToken applies the new password and retires every outstanding
// token of the account in one transaction. ok is false when the token is no
// longer usable.
//
// The user row is locked before any token row is touched, the same order
// issueResetToken uses. Two resets for one account therefore serialize on the
// user lock instead of deadlocking on each other's token rows.
func (h *UserAuthHandler) consumeResetToken(ctx context.Context, tokenHash, passwordHash string) (userID, email string, ok bool, err error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return "", "", false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx,
		`SELECT user_id::text FROM password_reset_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("finding token: %w", err)
	}

	var (
		status   string
		isActive bool
	)
	if err := tx.QueryRow(ctx,
		`SELECT email, status, is_active FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&email, &status, &isActive); err != nil {
		return "", "", false, fmt.Errorf("locking user: %w", err)
	}
	if !isActive || status == "suspended" {
		return "", "", false, nil
	}

	// The claim: exactly one concurrent caller can flip used_at for this token,
	// and it is re-evaluated after the user lock, so a use that committed while
	// this transaction waited is seen here.
	claimed, err := tx.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = NOW()
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()`, tokenHash)
	if err != nil {
		return "", "", false, fmt.Errorf("claiming token: %w", err)
	}
	if claimed.RowsAffected() != 1 {
		return "", "", false, nil
	}

	// password_changed_at is stamped from this process's clock, not NOW(): the
	// middleware compares it with a JWT iat that this same process minted, so
	// both sides of that comparison must come from one clock. Using the database
	// clock would let skew between the two hosts reject a login made right after
	// the reset.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, password_changed_at = $2, updated_at = NOW() WHERE id = $3`,
		passwordHash, time.Now(), userID,
	); err != nil {
		return "", "", false, fmt.Errorf("updating password: %w", err)
	}
	// Every other outstanding link for the account dies with this one.
	if _, err := tx.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = NOW() WHERE user_id = $1 AND used_at IS NULL`,
		userID,
	); err != nil {
		return "", "", false, fmt.Errorf("retiring other tokens: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", false, fmt.Errorf("commit: %w", err)
	}
	return userID, email, true, nil
}

// newResetToken returns a 256-bit URL-safe token and the hash that is stored in
// its place.
func newResetToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generating token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashResetToken(raw), nil
}

// hashResetToken is the SHA-256 hex digest under which a token is stored and
// looked up. The token is 256 random bits, so a fast hash is sufficient; the
// point is that a database read cannot be replayed as a working link.
func hashResetToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// validateNewPassword is the policy for a password chosen through reset.
func validateNewPassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("password must be at most %d bytes", maxPasswordBytes)
	}
	return nil
}

// normalizeResetLanguage maps whatever the client sends (including browser
// style tags such as "ko-KR") to a supported email language, defaulting to
// English.
func normalizeResetLanguage(raw string) lang.Code {
	l := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	switch code := lang.Code(l); code {
	case lang.Korean, lang.Chinese:
		return code
	}
	return lang.English
}

// resetLinkBase validates the configured panel URL and returns it without a
// trailing slash, or "" when it cannot safely anchor an emailed link. It is the
// only source of the link's host: request headers are never consulted.
func resetLinkBase(panelURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(panelURL), "/")
	u, err := url.Parse(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return trimmed
}
