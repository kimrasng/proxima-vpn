package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
)

// UserJWTMiddleware validates JWT tokens for user routes.
func UserJWTMiddleware(secret string) fiber.Handler {
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

		token, err := jwt.ParseWithClaims(tokenString, &handlers.UserClaims{}, func(t *jwt.Token) (interface{}, error) {
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

		claims, ok := token.Claims.(*handlers.UserClaims)
		if !ok || !token.Valid {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid token claims",
			})
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("status", claims.Status)

		return c.Next()
	}
}
