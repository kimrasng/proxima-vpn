package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
}

// NewAdminAuthHandler creates a new AdminAuthHandler.
func NewAdminAuthHandler(db *pgxpool.Pool, jwtSecret string, jwtExpiry time.Duration) *AdminAuthHandler {
	return &AdminAuthHandler{
		db:        db,
		jwtSecret: jwtSecret,
		jwtExpiry: jwtExpiry,
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
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	if !crypto.CheckPassword(passwordHash, req.Password) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	if totpEnabled {
		if req.TOTPCode == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "totp_code is required",
			})
		}
		if !crypto.ValidateTOTP(totpSecret, req.TOTPCode) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid totp code",
			})
		}
	}

	now := time.Now()
	claims := AdminClaims{
		AdminID: id,
		Email:   email,
		Role:    "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(h.jwtExpiry)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(h.jwtSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to generate token",
		})
	}

	return c.JSON(loginResponse{Token: tokenString})
}
