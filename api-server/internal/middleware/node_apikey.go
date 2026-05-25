package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NodeAPIKeyMiddleware validates the X-Node-Key header against the node's api_key.
func NodeAPIKeyMiddleware(db *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		apiKey := c.Get("X-Node-Key")
		if apiKey == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "missing X-Node-Key header",
			})
		}

		nodeID := c.Params("id")
		if nodeID == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "missing node id",
			})
		}

		var id string
		err := db.QueryRow(
			context.Background(),
			`SELECT id FROM nodes WHERE id = $1 AND api_key = $2 AND status != 'pending'`,
			nodeID, apiKey,
		).Scan(&id)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid node credentials",
			})
		}

		c.Locals("node_id", id)
		return c.Next()
	}
}
