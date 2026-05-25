package handlers

import (
	"context"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// UserClaims defines the JWT claims for user tokens.
type UserClaims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Status string `json:"status"`
	jwt.RegisteredClaims
}

// UserAuthHandler handles user authentication endpoints.
type UserAuthHandler struct {
	db        *pgxpool.Pool
	jwtSecret string
	jwtExpiry time.Duration
}

// NewUserAuthHandler creates a new UserAuthHandler.
func NewUserAuthHandler(db *pgxpool.Pool, jwtSecret string, jwtExpiry time.Duration) *UserAuthHandler {
	return &UserAuthHandler{
		db:        db,
		jwtSecret: jwtSecret,
		jwtExpiry: jwtExpiry,
	}
}

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type registerResponse struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	SubToken string `json:"sub_token"`
}

// Register handles user self-registration.
// @Summary User registration
// @Description Register a new user account
// @Tags user-auth
// @Accept json
// @Produce json
// @Param body body registerRequest true "Registration details"
// @Success 201 {object} registerResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /auth/register [post]
func (h *UserAuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Email == "" || req.Password == "" || req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "email, password, and name are required",
		})
	}

	if !emailRegex.MatchString(req.Email) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid email format",
		})
	}

	var exists bool
	err := h.db.QueryRow(
		context.Background(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`,
		req.Email,
	).Scan(&exists)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	if exists {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "email already registered",
		})
	}

	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	subToken := crypto.NewUUID()

	var id string
	err = h.db.QueryRow(
		context.Background(),
		`INSERT INTO users (email, password_hash, name, sub_token, status)
		 VALUES ($1, $2, $3, $4, 'pending')
		 RETURNING id`,
		req.Email, passwordHash, req.Name, subToken,
	).Scan(&id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(registerResponse{
		ID:       id,
		Email:    req.Email,
		Name:     req.Name,
		SubToken: subToken,
	})
}

type userLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login authenticates a user and returns a JWT token.
// @Summary User login
// @Description Authenticate user with email and password
// @Tags user-auth
// @Accept json
// @Produce json
// @Param body body userLoginRequest true "Login credentials"
// @Success 200 {object} loginResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /auth/login [post]
func (h *UserAuthHandler) Login(c *fiber.Ctx) error {
	var req userLoginRequest
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
		status       string
	)

	err := h.db.QueryRow(
		context.Background(),
		`SELECT id, email, password_hash, status FROM users WHERE email = $1`,
		req.Email,
	).Scan(&id, &email, &passwordHash, &status)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	if status == "suspended" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "account suspended",
		})
	}

	if !crypto.CheckPassword(passwordHash, req.Password) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	now := time.Now()
	claims := UserClaims{
		UserID: id,
		Email:  email,
		Status: status,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(h.jwtExpiry)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(h.jwtSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	return c.JSON(fiber.Map{
		"token": tokenString,
	})
}
