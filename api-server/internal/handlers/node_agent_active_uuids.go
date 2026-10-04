package handlers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"time"
)

func (h *NodeAgentHandler) BeginActiveUUIDGeneration(c *fiber.Ctx) error {
	id, ok := c.Locals("node_id").(string)
	parsed, err := uuid.Parse(c.Params("id"))
	if !ok || err != nil || parsed.String() != id {
		return c.SendStatus(401)
	}
	generation := c.Get("X-Report-Generation")
	if _, err := uuid.Parse(generation); err != nil {
		return c.SendStatus(400)
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
	defer cancel()
	var role, status string
	if err := h.db.QueryRow(ctx, `SELECT role,status FROM nodes WHERE id=$1`, id).Scan(&role, &status); err != nil {
		return c.SendStatus(503)
	}
	if status == "pending" || (role != "exit" && role != "both") {
		return c.SendStatus(403)
	}
	if err := services.BeginExitReportGeneration(ctx, h.redis, id, generation); err != nil {
		return c.SendStatus(503)
	}
	return c.SendStatus(204)
}

// ActiveUUIDReport is a lease of admitted, currently open egress associations.
// This is not Xray recent-packet activity: an idle but open VPN association
// continues holding its concurrency slot until it closes.
func (h *NodeAgentHandler) ActiveUUIDReport(c *fiber.Ctx) error {
	id, ok := c.Locals("node_id").(string)
	parsed, err := uuid.Parse(c.Params("id"))
	if !ok || err != nil || parsed.String() != id {
		return c.SendStatus(401)
	}
	var req struct {
		Generation string   `json:"generation"`
		Sequence   int64    `json:"sequence"`
		UUIDs      []string `json:"uuids"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil || req.UUIDs == nil || len(req.UUIDs) > 10000 {
		return c.SendStatus(400)
	}
	if req.Sequence <= 0 {
		return c.SendStatus(400)
	}
	if _, err := uuid.Parse(req.Generation); err != nil {
		return c.SendStatus(400)
	}
	uniq := make(map[string]bool, len(req.UUIDs))
	for _, raw := range req.UUIDs {
		u, err := uuid.Parse(raw)
		if err != nil || uniq[u.String()] {
			return c.SendStatus(400)
		}
		uniq[u.String()] = true
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
	defer cancel()
	// A lease is accepted only from the authenticated Exit, never a relay or a
	// pending node. Existing invalidated assignments cannot be revived by report.
	var role, status string
	if err := h.db.QueryRow(ctx, `SELECT role,status FROM nodes WHERE id=$1`, id).Scan(&role, &status); err != nil {
		return c.SendStatus(503)
	}
	if status == "pending" || (role != "exit" && role != "both") {
		return c.SendStatus(403)
	}
	if err := services.PublishAdmittedUUIDReport(ctx, h.redis, id, req.Generation, req.Sequence, req.UUIDs, time.Now()); err != nil {
		return c.SendStatus(503)
	}
	c.Set("Cache-Control", "no-store")
	return c.SendStatus(204)
}
