package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	netmail "net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

const (
	signupCodeTTL              = 10 * time.Minute
	signupCodeResendCooldown   = 60 * time.Second
	signupCodeMaxAttempts      = 5
	signupCodeMaxRequestsPerIP = 5
)

var signupCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

type signupVerificationRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	Name         string `json:"name"`
	ReferralCode string `json:"referral_code"`
	Language     string `json:"language"`
	Code         string `json:"code"`
}

type pendingSignup struct {
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"`
	Name         string `json:"name"`
	ReferralCode string `json:"referral_code,omitempty"`
	Language     string `json:"language,omitempty"`
}

func (h *UserAuthHandler) signupCodeHash(email, code string) string {
	mac := hmac.New(sha256.New, []byte(h.jwtSecret))
	_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(email)) + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

func signupCode(ctx context.Context) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return fmt.Sprintf("%06d", v%1000000), nil
}

func validateSignupInput(c *fiber.Ctx, h *UserAuthHandler, email, password, name string) error {
	if email == "" || password == "" || name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email, password, and name are required"})
	}
	if !emailRegex.MatchString(email) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email format"})
	}
	if on, allowed := loadSignupPolicy(c.UserContext(), h.db); !signupDomainAllowed(email, on, allowed) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "this email domain is not allowed to register", "allowed_domains": allowed})
	}
	var exists bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=lower($1))`, email).Scan(&exists); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if exists {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already registered"})
	}
	if h.mailer == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "email verification is unavailable"})
	}
	return nil
}

// RequestSignupEmailCode validates the registration details, stores a short-lived
// verification challenge, and emails a six-digit code. No account is created yet.
func (h *UserAuthHandler) RequestSignupEmailCode(c *fiber.Ctx) error {
	if h.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "email verification is unavailable"})
	}
	var disabled bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT value='false' FROM settings WHERE key='self_registration'`).Scan(&disabled); err == nil && disabled {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "self-registration is disabled"})
	}
	var req signupVerificationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	if err := validateSignupInput(c, h, req.Email, req.Password, req.Name); err != nil {
		return err
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "password must be 8-72 characters"})
	}
	if len(req.Name) > 128 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is too long"})
	}
	if _, err := netmail.ParseAddress(req.Email); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email format"})
	}
	if req.ReferralCode != "" && len(req.ReferralCode) > 128 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "referral code is too long"})
	}
	var sentAt time.Time
	err := h.db.QueryRow(c.UserContext(), `SELECT sent_at FROM signup_email_verifications WHERE lower(email)=lower($1)`, req.Email).Scan(&sentAt)
	if err == nil && time.Since(sentAt) < signupCodeResendCooldown {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "please wait before requesting another code"})
	}
	if err != nil && err != pgx.ErrNoRows {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	code, err := signupCode(c.UserContext())
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	payload, err := json.Marshal(pendingSignup{Email: req.Email, PasswordHash: hash, Name: req.Name, ReferralCode: req.ReferralCode, Language: req.Language})
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	tx, err := h.db.Begin(c.UserContext())
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	defer func() { _ = tx.Rollback(c.UserContext()) }()
	_, err = tx.Exec(c.UserContext(), `INSERT INTO signup_email_verifications(email,code_hash,payload,expires_at,sent_at,attempts) VALUES($1,$2,$3,$4,NOW(),0) ON CONFLICT(email) DO UPDATE SET code_hash=EXCLUDED.code_hash,payload=EXCLUDED.payload,expires_at=EXCLUDED.expires_at,sent_at=NOW(),attempts=0`, req.Email, h.signupCodeHash(req.Email, code), string(payload), time.Now().Add(signupCodeTTL))
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if err = h.mailer.Send(c.UserContext(), mail.Message{To: req.Email, Subject: signupVerificationSubject(req.Language), Body: signupVerificationBody(req.Language, code)}); err != nil {
		log.Printf("signup verification email failed: %v", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "could not send verification email"})
	}
	if err = tx.Commit(c.UserContext()); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	return c.JSON(fiber.Map{"message": "verification code sent", "expires_in_seconds": int(signupCodeTTL.Seconds())})
}

// VerifySignupEmailCode checks the one-time code and creates the account only
// after a successful match. The code is consumed atomically with account creation.
func (h *UserAuthHandler) VerifySignupEmailCode(c *fiber.Ctx) error {
	if h.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "email verification is unavailable"})
	}
	var req signupVerificationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Code = strings.TrimSpace(req.Code)
	if req.Email == "" || !signupCodePattern.MatchString(req.Code) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "enter the 6-digit verification code"})
	}
	tx, err := h.db.Begin(c.UserContext())
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	defer func() { _ = tx.Rollback(c.UserContext()) }()
	var storedHash string
	var payloadRaw []byte
	var expires time.Time
	var attempts int
	err = tx.QueryRow(c.UserContext(), `SELECT code_hash,payload,expires_at,attempts FROM signup_email_verifications WHERE lower(email)=lower($1) FOR UPDATE`, req.Email).Scan(&storedHash, &payloadRaw, &expires, &attempts)
	if err == pgx.ErrNoRows {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "verification code expired; request a new one"})
	}
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if time.Now().After(expires) {
		_, _ = tx.Exec(c.UserContext(), `DELETE FROM signup_email_verifications WHERE lower(email)=lower($1)`, req.Email)
		_ = tx.Commit(c.UserContext())
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "verification code expired; request a new one"})
	}
	if attempts >= signupCodeMaxAttempts {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "too many incorrect codes; request a new one"})
	}
	expected := h.signupCodeHash(req.Email, req.Code)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(expected)) != 1 {
		_, _ = tx.Exec(c.UserContext(), `UPDATE signup_email_verifications SET attempts=attempts+1 WHERE lower(email)=lower($1)`, req.Email)
		_ = tx.Commit(c.UserContext())
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "incorrect verification code"})
	}
	var pending pendingSignup
	if err = json.Unmarshal(payloadRaw, &pending); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	var disabled bool
	if err := tx.QueryRow(c.UserContext(), `SELECT value='false' FROM settings WHERE key='self_registration'`).Scan(&disabled); err == nil && disabled {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "self-registration is disabled"})
	}
	if on, allowed := loadSignupPolicy(c.UserContext(), h.db); !signupDomainAllowed(pending.Email, on, allowed) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "this email domain is not allowed to register", "allowed_domains": allowed})
	}
	var exists bool
	if err = tx.QueryRow(c.UserContext(), `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=lower($1))`, pending.Email).Scan(&exists); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if exists {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already registered"})
	}
	subToken := crypto.NewUUID()
	var id string
	err = tx.QueryRow(c.UserContext(), `INSERT INTO users(email,password_hash,name,sub_token,status) VALUES($1,$2,$3,$4,'pending') RETURNING id::text`, pending.Email, pending.PasswordHash, pending.Name, subToken).Scan(&id)
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if _, err = tx.Exec(c.UserContext(), `DELETE FROM signup_email_verifications WHERE lower(email)=lower($1)`, pending.Email); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if err = tx.Commit(c.UserContext()); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	h.activity.Log(c.UserContext(), services.Record{EventType: services.EventUserRegistered, Severity: services.SeverityInfo, ActorType: "user", ActorID: id, ActorLabel: pending.Name, TargetType: "user", TargetID: id, Detail: map[string]any{"email": pending.Email}})
	if h.telegram != nil {
		go func(email string) {
			if e := h.telegram.NotifyNewRegistration(context.Background(), email); e != nil {
				log.Printf("telegram registration alert: %v", e)
			}
		}(pending.Email)
	}
	return c.Status(fiber.StatusCreated).JSON(registerResponse{ID: id, Email: pending.Email, Name: pending.Name, SubToken: subToken})
}
