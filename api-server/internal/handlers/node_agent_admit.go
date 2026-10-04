package handlers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"time"
)

// AdmitDevice reserves an account-wide UUID slot before a node-local SOCKS
// connection receives success. Payload permits remain independent.
func (h *NodeAgentHandler) AdmitDevice(c *fiber.Ctx) error {
	nodeID, ok := c.Locals("node_id").(string)
	parsed, err := uuid.Parse(c.Params("id"))
	if !ok || err != nil || parsed.String() != nodeID {
		return c.SendStatus(401)
	}
	var req struct {
		DeviceUUID string `json:"device_uuid"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.SendStatus(400)
	}
	device, err := uuid.Parse(req.DeviceUUID)
	if err != nil {
		return c.SendStatus(400)
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
	defer cancel()
	allowed, err := services.NewDeviceBandwidthService(h.db, h.redis).AdmitDevice(ctx, nodeID, device.String())
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "device admission unavailable"})
	}
	c.Set("Cache-Control", "no-store")
	if !allowed {
		return c.Status(409).JSON(fiber.Map{"error": "online UUID limit reached"})
	}
	return c.SendStatus(204)
}
