package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
)

func AdminJWTMiddleware(secret string, db *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "missing authorization header",
			})
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid authorization format",
			})
		}

		tokenString := parts[1]

		token, err := jwt.ParseWithClaims(tokenString, &handlers.AdminClaims{}, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fiber.NewError(fiber.StatusUnauthorized, "unexpected signing method")
			}
			return []byte(secret), nil
		})
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid or expired token",
			})
		}

		claims, ok := token.Claims.(*handlers.AdminClaims)
		if !ok || !token.Valid {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid token claims",
			})
		}

		// Role, status and email are read fresh on every request, so demoting,
		// disabling or renaming an admin takes effect immediately instead of
		// when the token expires.
		var (
			email     string
			role      string
			isActive  bool
			pwChanged time.Time
		)
		err = db.QueryRow(context.Background(),
			"SELECT email, role, is_active, password_changed_at FROM admins WHERE id = $1", claims.AdminID,
		).Scan(&email, &role, &isActive, &pwChanged)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "admin account not found",
			})
		}
		if !isActive {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "admin account is disabled",
			})
		}
		if claims.PasswordChangedAt != pwChanged.UnixMicro() {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "session ended after a password change",
			})
		}

		c.Locals("admin_id", claims.AdminID)
		c.Locals("email", email)
		c.Locals("admin_role", role)

		return c.Next()
	}
}

// RequireAdminOwner allows the request only for admins whose current role is
// owner. It must run after AdminJWTMiddleware, which loads the role.
func RequireAdminOwner() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if role, _ := c.Locals("admin_role").(string); role != "owner" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "only an owner can manage admin accounts",
			})
		}
		return c.Next()
	}
}
