package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

type nodeBandwidthPermitRequest = devicebandwidth.PermitRequest

// BandwidthPermit is node-authenticated, but never trusts a node's claimed rate
// or path identity. Each device's plan and eligibility are read for every grant.
func (h *NodeAgentHandler) BandwidthPermit(c *fiber.Ctx) error {
	nodeID, ok := c.Locals("node_id").(string)
	pathNode, pathErr := uuid.Parse(c.Params("id"))
	authNode, authErr := uuid.Parse(nodeID)
	if !ok || pathErr != nil || authErr != nil || pathNode != authNode {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid node identity"})
	}

	var req nodeBandwidthPermitRequest
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if _, err := uuid.Parse(req.DeviceUUID); err != nil || (req.Direction != "upload" && req.Direction != "download") || req.Bytes < 1 || req.Bytes > services.MaxDeviceBandwidthBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid bandwidth permit request"})
	}
	if h.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bandwidth permit unavailable"})
	}

	ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
	defer cancel()
	permit, err := services.NewDeviceBandwidthService(h.db, h.redis).Permit(ctx, nodeID, req.DeviceUUID, string(req.Direction), req.Bytes)
	if errors.Is(err, services.ErrInvalidBandwidthRequest) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid bandwidth permit request"})
	}
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bandwidth permit unavailable"})
	}
	return c.JSON(permit)
}
