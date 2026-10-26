package middleware

import (
	"errors"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// OAuthBearerMiddleware authenticates /api/v1/client requests with an OAuth
// access token and sets the same locals the portal JWT middleware sets, so the
// existing user handlers serve both. Errors follow RFC 6750.
func OAuthBearerMiddleware(svc *services.OAuthService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		header := c.Get(fiber.HeaderAuthorization)
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
			c.Set(fiber.HeaderWWWAuthenticate, `Bearer realm="proxima"`)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid_request", "error_description": "missing bearer token"})
		}
		bearer, err := svc.AuthenticateBearer(c.Context(), token)
		if err != nil {
			var oe *services.OAuthError
			if errors.As(err, &oe) {
				c.Set(fiber.HeaderWWWAuthenticate, `Bearer realm="proxima", error="invalid_token"`)
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": oe.Code, "error_description": oe.Description})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "server_error"})
		}
		c.Locals("user_id", bearer.UserID)
		c.Locals("email", bearer.Email)
		c.Locals("status", bearer.Status)
		c.Locals("oauth_client_id", bearer.ClientID)
		c.Locals("oauth_scopes", bearer.Scopes)
		return c.Next()
	}
}

// RequireScope rejects a token that was not granted scope (RFC 6750 3.1).
func RequireScope(scope string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		scopes, _ := c.Locals("oauth_scopes").([]string)
		if !slices.Contains(scopes, scope) {
			c.Set(fiber.HeaderWWWAuthenticate, `Bearer realm="proxima", error="insufficient_scope", scope="`+scope+`"`)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "insufficient_scope", "error_description": "this token lacks the " + scope + " scope", "scope": scope})
		}
		return c.Next()
	}
}
