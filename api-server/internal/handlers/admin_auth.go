package handlers

import (
	"context"
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
	)

	err := h.db.QueryRow(
		context.Background(),
		`SELECT id, email, password_hash, totp_secret, totp_enabled
		 FROM admins WHERE email = $1`,
		req.Email,
	).Scan(&id, &email, &passwordHash, &totpSecret, &totpEnabled)
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
		Role:    "admin",
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
