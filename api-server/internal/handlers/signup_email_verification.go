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
	"math/big"
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
	signupCodeTTL            = 10 * time.Minute
	signupCodeResendCooldown = 60 * time.Second
	signupCodeMaxAttempts    = 5
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

func signupCode() (string, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", v.Int64()), nil
}

func validateSignupInput(email, password, name string) error {
	if email == "" || password == "" || name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email, password, and name are required")
	}
	if !emailRegex.MatchString(email) {
		return fiber.NewError(fiber.StatusBadRequest, "invalid email format")
	}
	if len(password) < 8 || len(password) > 72 {
		return fiber.NewError(fiber.StatusBadRequest, "password must be 8-72 characters")
	}
	if len(name) > 128 {
		return fiber.NewError(fiber.StatusBadRequest, "name is too long")
	}
	if _, err := netmail.ParseAddress(email); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid email format")
	}
	return nil
}

// The boolean is explicit: JSON responses usually return nil, which must never
// be mistaken for permission to continue. Read policy through the current
// transaction when verifying, and fail closed on database errors.
func checkSignupPolicy(c *fiber.Ctx, db settingsQuerier, email string) (bool, error) {
	var registration, restriction, domains string
	err := db.QueryRow(c.UserContext(), `SELECT
		COALESCE((SELECT value FROM settings WHERE key='self_registration'), 'true'),
		COALESCE((SELECT value FROM settings WHERE key=$1), 'false'),
		COALESCE((SELECT value FROM settings WHERE key=$2), '')`, settingSignupDomainRestriction, settingSignupEmailDomains).Scan(&registration, &restriction, &domains)
	if err != nil {
		return false, c.SendStatus(fiber.StatusInternalServerError)
	}
	if strings.EqualFold(strings.TrimSpace(registration), "false") {
		return false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "self-registration is disabled"})
	}
	allowed := normalizeEmailDomains(domains)
	if !signupDomainAllowed(email, strings.EqualFold(strings.TrimSpace(restriction), "true"), allowed) {
		return false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "this email domain is not allowed to register", "allowed_domains": allowed})
	}
	return true, nil
}

// RequestSignupEmailCode validates the registration details, stores a short-lived
// verification challenge, and emails a six-digit code. No account is created yet.
func (h *UserAuthHandler) RequestSignupEmailCode(c *fiber.Ctx) error {
	if h.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "email verification is unavailable"})
	}
	var req signupVerificationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	if err := validateSignupInput(req.Email, req.Password, req.Name); err != nil {
		return err
	}
	if ok, err := checkSignupPolicy(c, h.db, req.Email); !ok {
		return err
	}
	var exists bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=$1)`, req.Email).Scan(&exists); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if exists {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already registered"})
	}
	if h.mailer == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "email verification is unavailable"})
	}
	if req.ReferralCode != "" && len(req.ReferralCode) > 128 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "referral code is too long"})
	}
	code, err := signupCode()
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
	result, err := tx.Exec(c.UserContext(), `INSERT INTO signup_email_verifications(email,code_hash,payload,expires_at,sent_at,attempts) VALUES($1,$2,$3,$4,NOW(),0) ON CONFLICT(email) DO UPDATE SET code_hash=EXCLUDED.code_hash,payload=EXCLUDED.payload,expires_at=EXCLUDED.expires_at,sent_at=NOW(),attempts=0 WHERE signup_email_verifications.sent_at <= NOW() - $5 * INTERVAL '1 second'`, req.Email, h.signupCodeHash(req.Email, code), string(payload), time.Now().Add(signupCodeTTL), int(signupCodeResendCooldown.Seconds()))
	if err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "please wait before requesting another code"})
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
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
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
	if !time.Now().Before(expires) {
		if _, err = tx.Exec(c.UserContext(), `DELETE FROM signup_email_verifications WHERE lower(email)=lower($1)`, req.Email); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if err = tx.Commit(c.UserContext()); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "verification code expired; request a new one"})
	}
	if attempts >= signupCodeMaxAttempts {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "too many incorrect codes; request a new one"})
	}
	expected := h.signupCodeHash(req.Email, req.Code)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(expected)) != 1 {
		if _, err = tx.Exec(c.UserContext(), `UPDATE signup_email_verifications SET attempts=attempts+1 WHERE lower(email)=lower($1)`, req.Email); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if err = tx.Commit(c.UserContext()); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "incorrect verification code"})
	}
	var pending pendingSignup
	if err = json.Unmarshal(payloadRaw, &pending); err != nil {
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if ok, err := checkSignupPolicy(c, tx, pending.Email); !ok {
		return err
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
