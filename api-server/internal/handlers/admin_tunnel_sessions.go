package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/redis/go-redis/v9"
)

// AdminTunnelSessionHandler displays authenticated live transport counts.
// This is read-only: the default runtime has not enabled session enforcement.
type AdminTunnelSessionHandler struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

func NewAdminTunnelSessionHandler(db *pgxpool.Pool, rdb *redis.Client) *AdminTunnelSessionHandler {
	return &AdminTunnelSessionHandler{db: db, redis: rdb}
}
func (h *AdminTunnelSessionHandler) Get(c *fiber.Ctx) error {
	var exists bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT EXISTS(SELECT 1 FROM users WHERE id::text=$1)`, c.Params("id")).Scan(&exists); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "session observation unavailable"})
	}
	if !exists {
		return c.Status(404).JSON(fiber.Map{"error": "user not found"})
	}
	observation, snapshot, err := services.ObserveAccountTunnelSessions(c.UserContext(), h.db, h.redis, c.Params("id"))
	if err != nil || !observation.Complete {
		return c.JSON(fiber.Map{"complete": false, "count": nil, "sessions": []any{}, "enforced": false})
	}
	count, err := services.CountTunnelSessions(observation)
	if err != nil {
		return c.JSON(fiber.Map{"complete": false, "count": nil, "sessions": []any{}, "enforced": false})
	}
	// Expose aggregate only. Raw peer tuples identify source addresses and
	// should not be included in this summary endpoint.
	return c.JSON(fiber.Map{"complete": true, "count": count, "report_id": snapshot, "enforced": false})
}
