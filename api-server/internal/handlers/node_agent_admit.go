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
		DeviceUUID    string `json:"device_uuid"`
		SourceAddress string `json:"source_address"`
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
	service := services.NewDeviceBandwidthService(h.db, h.redis)
	var allowed bool
	if req.SourceAddress != "" {
		allowed, err = service.AdmitSourceDevice(ctx, nodeID, device.String(), req.SourceAddress)
	} else {
		allowed, err = service.AdmitDevice(ctx, nodeID, device.String())
	}
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "device admission unavailable"})
	}
	c.Set("Cache-Control", "no-store")
	if !allowed {
		message := "online UUID limit reached"
		if req.SourceAddress != "" {
			message = "online address limit reached"
		}
		return c.Status(409).JSON(fiber.Map{"error": message})
	}
	return c.SendStatus(204)
}
