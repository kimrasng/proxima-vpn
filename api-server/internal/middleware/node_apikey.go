package middleware

import (
	"context"
	"crypto/subtle"

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

		// Compare the key in application code with a constant-time comparison
		// rather than folding it into the SQL WHERE clause: a SQL btree
		// equality scan isn't guaranteed constant-time, and doing the compare
		// here removes any doubt.
		var id, storedKey string
		err := db.QueryRow(
			context.Background(),
			`SELECT id, api_key FROM nodes WHERE id = $1 AND status != 'pending'`,
			nodeID,
		).Scan(&id, &storedKey)
		if err != nil || subtle.ConstantTimeCompare([]byte(storedKey), []byte(apiKey)) != 1 {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid node credentials",
			})
		}

		c.Locals("node_id", id)
		return c.Next()
	}
}
