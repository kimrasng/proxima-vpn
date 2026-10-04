package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminUUIDEvictionHandler struct{ db *pgxpool.Pool }

func NewAdminUUIDEvictionHandler(db *pgxpool.Pool) *AdminUUIDEvictionHandler {
	return &AdminUUIDEvictionHandler{db: db}
}

type uuidEvictionStatus struct {
	DeviceUUID          string            `json:"device_uuid"`
	Epoch               string            `json:"epoch"`
	State               string            `json:"state"`
	RequestedAt         time.Time         `json:"requested_at"`
	ConfirmedAt         *time.Time        `json:"confirmed_at"`
	RequiredNodeIDs     []string          `json:"required_node_ids"`
	AcknowledgedNodeIDs []string          `json:"acknowledged_node_ids"`
	PendingNodeIDs      []string          `json:"pending_node_ids"`
	NodeNames           map[string]string `json:"node_names"`
}

// List returns the durable eviction state, including Exits that have not yet
// acknowledged closure. Read-only: it cannot release a still-connected UUID.
func (h *AdminUUIDEvictionHandler) List(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 5*time.Second)
	defer cancel()
	rows, err := h.db.Query(ctx, `SELECT e.device_uuid,e.epoch::text,e.status,e.requested_at,e.confirmed_at,
 ARRAY(SELECT x::text FROM unnest(e.required_node_ids) x ORDER BY x),
 ARRAY(SELECT a.node_id::text FROM uuid_eviction_acknowledgments a WHERE a.epoch=e.epoch ORDER BY a.node_id),
 COALESCE((SELECT jsonb_object_agg(n.id::text,n.name) FROM nodes n WHERE n.id=ANY(e.required_node_ids)),'{}'::jsonb)
 FROM uuid_evictions e ORDER BY e.requested_at DESC LIMIT 200`)
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "eviction state unavailable"})
	}
	defer rows.Close()
	entries := make([]uuidEvictionStatus, 0)
	for rows.Next() {
		var item uuidEvictionStatus
		if err := rows.Scan(&item.DeviceUUID, &item.Epoch, &item.State, &item.RequestedAt, &item.ConfirmedAt, &item.RequiredNodeIDs, &item.AcknowledgedNodeIDs, &item.NodeNames); err != nil {
			return c.Status(503).JSON(fiber.Map{"error": "eviction state unavailable"})
		}
		ack := map[string]bool{}
		for _, node := range item.AcknowledgedNodeIDs {
			ack[node] = true
		}
		item.PendingNodeIDs = []string{}
		for _, node := range item.RequiredNodeIDs {
			if !ack[node] {
				item.PendingNodeIDs = append(item.PendingNodeIDs, node)
			}
		}
		entries = append(entries, item)
	}
	if rows.Err() != nil {
		return c.Status(503).JSON(fiber.Map{"error": "eviction state unavailable"})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(entries)
}

// Retry keeps the existing epoch and ban intact, allowing healthy agents to
// poll and re-ack. It never forges a missing agent acknowledgment.
func (h *AdminUUIDEvictionHandler) Retry(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("uuid"))
	if err != nil {
		return c.SendStatus(400)
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 5*time.Second)
	defer cancel()
	var state string
	err = h.db.QueryRow(ctx, `SELECT status FROM uuid_evictions WHERE device_uuid=$1`, id.String()).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.SendStatus(404)
	}
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "eviction state unavailable"})
	}
	if state != "pending" {
		return c.Status(409).JSON(fiber.Map{"error": "eviction is not pending"})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"state": "pending", "message": "ban and epoch retained; agents will retry acknowledgments"})
}
