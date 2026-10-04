package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/database"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// RevokedDevices is an authenticated fleet-wide revocation snapshot. A node
// agent can close controlled TCP/UDP sessions without waiting for Xray config
// polling; subscription/device assignment and permits still check the DB.
func (h *NodeAgentHandler) RevokedDevices(c *fiber.Ctx) error {
	id, ok := c.Locals("node_id").(string)
	parsed, err := uuid.Parse(c.Params("id"))
	if !ok || err != nil || parsed.String() != id {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	// This snapshot deliberately covers all Exits: a UUID can remain connected
	// to an Exit from a former plan group after assignments change.
	ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
	defer cancel()
	rows, err := h.db.Query(ctx, `SELECT uuid, epoch FROM (
   SELECT d.xray_uuid AS uuid, NULL::text AS epoch FROM devices d
   JOIN users u ON u.id = d.user_id
   LEFT JOIN plans p ON p.id = u.plan_id
   WHERE d.evicted_until > NOW() OR d.retired_at IS NOT NULL
      OR u.is_active = false OR u.status <> 'active'
      OR u.plan_expires_at <= NOW()
      OR p.id IS NULL OR p.is_active = false
      OR (p.traffic_limit IS NOT NULL AND u.traffic_used >= p.traffic_limit)
   UNION ALL
   SELECT device_uuid, epoch::text FROM uuid_evictions
   WHERE status='pending' OR status='confirmed'
  ) revoked ORDER BY uuid`)
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "revocation snapshot unavailable"})
	}
	defer rows.Close()
	uuids := make([]string, 0)
	revocations := make([]fiber.Map, 0)
	seen := make(map[string]bool)
	for rows.Next() {
		var device string
		var epoch *string
		if err := rows.Scan(&device, &epoch); err != nil {
			return c.Status(503).JSON(fiber.Map{"error": "revocation snapshot unavailable"})
		}
		if !seen[device] {
			uuids = append(uuids, device)
			seen[device] = true
		}
		if epoch != nil {
			revocations = append(revocations, fiber.Map{"uuid": device, "epoch": *epoch})
		}
	}
	if rows.Err() != nil {
		return c.Status(503).JSON(fiber.Map{"error": "revocation snapshot unavailable"})
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"revoked_uuids": uuids, "revocations": revocations})
}

// AcknowledgeRevocation records closure only for the authenticated node and
// the current epoch. An obsolete or unrelated acknowledgment cannot confirm it.
func (h *NodeAgentHandler) AcknowledgeRevocation(c *fiber.Ctx) error {
	id, ok := c.Locals("node_id").(string)
	parsed, err := uuid.Parse(c.Params("id"))
	if !ok || err != nil || parsed.String() != id {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	var body struct {
		UUID   string `json:"uuid"`
		Epoch  string `json:"epoch"`
		NodeID string `json:"node_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.SendStatus(fiber.StatusBadRequest)
	}
	device, err := uuid.Parse(body.UUID)
	if err != nil {
		return c.SendStatus(fiber.StatusBadRequest)
	}
	epoch, err := uuid.Parse(body.Epoch)
	if err != nil || (body.NodeID != "" && body.NodeID != id) {
		return c.SendStatus(fiber.StatusBadRequest)
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
	defer cancel()
	err = database.AcknowledgeUUIDEviction(ctx, h.db, device.String(), epoch.String(), id)
	if errors.Is(err, database.ErrEvictionEpoch) {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "stale eviction epoch"})
	}
	if errors.Is(err, database.ErrEvictionNode) {
		return c.SendStatus(fiber.StatusForbidden)
	}
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "eviction acknowledgment unavailable"})
	}
	c.Set("Cache-Control", "no-store")
	return c.SendStatus(fiber.StatusNoContent)
}
