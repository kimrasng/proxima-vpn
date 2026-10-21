package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// GetConcurrencyEnforcement reports whether the concurrency scheduler evicts
// over-cap online UUID slots. It reads the same switch the scheduler does, so
// the panel never claims blocking while the server only observes (or vice
// versa). It is not proof that any particular eviction happened.
func GetConcurrencyEnforcement(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"enforced": services.ConcurrencyEnforcementEnabled()})
}
