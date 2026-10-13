package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
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
	var limit int
	if err := h.db.QueryRow(c.UserContext(), `SELECT COALESCE(p.max_concurrent,p.max_devices,0)
	 FROM users u LEFT JOIN plans p ON p.id=u.plan_id WHERE u.id::text=$1`, c.Params("id")).Scan(&limit); err != nil {
		if err == pgx.ErrNoRows {
			return c.Status(404).JSON(fiber.Map{"error": "user not found"})
		}
		return c.Status(500).JSON(fiber.Map{"error": "session observation unavailable"})
	}
	observation, snapshot, err := services.ObserveAccountTunnelSessions(c.UserContext(), h.db, h.redis, c.Params("id"))
	if err != nil || !observation.Complete {
		return c.JSON(fiber.Map{"complete": false, "count": nil, "limit": limit, "over_limit": nil, "enforced": false})
	}
	count, err := services.CountTunnelSessions(observation)
	if err != nil {
		return c.JSON(fiber.Map{"complete": false, "count": nil, "limit": limit, "over_limit": nil, "enforced": false})
	}
	// Expose aggregate only. Raw peer tuples identify source addresses and
	// should not be included in this summary endpoint.
	return c.JSON(fiber.Map{"complete": true, "count": count, "limit": limit, "over_limit": limit > 0 && count > limit, "report_id": snapshot, "enforced": false})
}
