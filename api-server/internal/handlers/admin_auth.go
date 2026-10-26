package handlers

import (
	"context"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// AdminClaims defines the JWT claims for admin tokens.
type AdminClaims struct {
	AdminID string `json:"admin_id"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	// PasswordChangedAt is admins.password_changed_at (Unix microseconds) at
	// issue time. The middleware rejects the token once the stored value moves,
	// so changing or resetting a password ends every older session exactly.
	PasswordChangedAt int64 `json:"pwc"`
	jwt.RegisteredClaims
}

// AdminAuthHandler handles admin authentication endpoints.
type AdminAuthHandler struct {
	db        *pgxpool.Pool
	jwtSecret string
	jwtExpiry time.Duration
	activity  *services.ActivityService
	logins    *services.LoginHistoryService
}

// NewAdminAuthHandler creates a new AdminAuthHandler.
func NewAdminAuthHandler(db *pgxpool.Pool, jwtSecret string, jwtExpiry time.Duration) *AdminAuthHandler {
	return &AdminAuthHandler{
		db:        db,
		jwtSecret: jwtSecret,
		jwtExpiry: jwtExpiry,
		activity:  services.NewActivityService(db),
		logins:    services.NewLoginHistoryService(db),
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

type loginResponse struct {
	Token string `json:"token"`
}

// Login authenticates an admin and returns a JWT token.
// @Summary Admin login
// @Description Authenticate admin with email/password and optional TOTP
// @Tags admin-auth
// @Accept json
// @Produce json
// @Param body body loginRequest true "Login credentials"
// @Success 200 {object} loginResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /admin/auth/login [post]
func (h *AdminAuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "email and password are required",
		})
	}

	var (
		id           string
		email        string
		passwordHash string
		totpSecret   string
		totpEnabled  bool
		role         string
		isActive     bool
		pwChanged    time.Time
	)

	err := h.db.QueryRow(
		context.Background(),
		`SELECT id, email, password_hash, COALESCE(totp_secret, ''), totp_enabled, role, is_active, password_changed_at
		 FROM admins WHERE lower(email) = lower($1)`,
		req.Email,
	).Scan(&id, &email, &passwordHash, &totpSecret, &totpEnabled, &role, &isActive, &pwChanged)
	if err != nil {
		h.recordLogin(c, req.Email, false, services.LoginFailureUnknownEmail)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	if !crypto.CheckPassword(passwordHash, req.Password) {
		h.recordLogin(c, email, false, services.LoginFailureBadPassword)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	if !isActive {
		h.recordLogin(c, email, false, services.LoginFailureSuspended)
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "admin account is disabled",
		})
	}

	if totpEnabled {
		if req.TOTPCode == "" {
			h.recordLogin(c, email, false, services.LoginFailureBadTOTP)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "totp_code is required",
			})
		}
		if !crypto.ValidateTOTP(totpSecret, req.TOTPCode) {
			h.recordLogin(c, email, false, services.LoginFailureBadTOTP)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid totp code",
			})
		}
	}

	now := time.Now()
	expiry := resolveSessionExpiry(context.Background(), h.db, h.jwtExpiry)
	claims := AdminClaims{
		AdminID: id,
		Email:   email,
		Role:    role,

		PasswordChangedAt: pwChanged.UnixMicro(),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(h.jwtSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to generate token",
		})
	}

	if _, err := h.db.Exec(context.Background(), `UPDATE admins SET last_login_at = NOW() WHERE id = $1`, id); err != nil {
		log.Printf("[admin-auth] record last login: %v", err)
	}

	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventAdminLogin,
		Severity:   services.SeverityInfo,
		ActorType:  "admin",
		ActorID:    id,
		ActorLabel: email,
		Detail:     map[string]any{"ip": c.IP()},
	})
	h.recordLogin(c, email, true, "")

	return c.JSON(loginResponse{Token: tokenString})
}

// recordLogin persists an admin attempt. user_id is left null on purpose: it is
// a foreign key into users, and an admin id is not a user id. Admin attempts are
// identified by attempted_email plus actor_type.
func (h *AdminAuthHandler) recordLogin(c *fiber.Ctx, email string, success bool, reason string) {
	h.logins.Record(context.Background(), services.LoginAttempt{
		ActorType: services.LoginActorAdmin,
		Email:     email,
		Success:   success,
		Reason:    reason,
		IP:        c.IP(),
		UserAgent: c.Get(fiber.HeaderUserAgent),
	})
}

// IssueToken signs a fresh admin JWT bound to the admin's current role and
// password version. It is used after a password change so the session that
// made the change stays signed in while every older token is rejected.
func (h *AdminAuthHandler) IssueToken(c *fiber.Ctx, adminID string) (string, error) {
	var (
		email     string
		role      string
		pwChanged time.Time
	)
	ctx := c.UserContext()
	if err := h.db.QueryRow(ctx, `SELECT email, role, password_changed_at FROM admins WHERE id = $1`, adminID).Scan(&email, &role, &pwChanged); err != nil {
		return "", err
	}
	now := time.Now()
	claims := AdminClaims{
		AdminID:           adminID,
		Email:             email,
		Role:              role,
		PasswordChangedAt: pwChanged.UnixMicro(),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(resolveSessionExpiry(ctx, h.db, h.jwtExpiry))),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(h.jwtSecret))
}
