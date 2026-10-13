package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/redis/go-redis/v9"
)

// AdminClientAddressHandler exposes observation only; incomplete evidence must
// not be displayed as zero clients or used to evict a shared account UUID.
type AdminClientAddressHandler struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

func NewAdminClientAddressHandler(db *pgxpool.Pool, rdb *redis.Client) *AdminClientAddressHandler {
	return &AdminClientAddressHandler{db: db, redis: rdb}
}

func (h *AdminClientAddressHandler) Get(c *fiber.Ctx) error {
	var exists bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT EXISTS(SELECT 1 FROM users WHERE id::text=$1)`, c.Params("id")).Scan(&exists); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "client observation unavailable"})
	}
	if !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}
	observation, err := services.ObserveClientAddresses(c.UserContext(), h.db, h.redis, c.Params("id"))
	if err != nil {
		return c.JSON(fiber.Map{"complete": false, "addresses": []string{}, "count": nil})
	}
	var count any
	if observation.Complete {
		count = len(observation.Addresses)
	}
	return c.JSON(fiber.Map{"complete": observation.Complete, "addresses": observation.Addresses, "count": count})
}
