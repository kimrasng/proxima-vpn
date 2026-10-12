package handlers

import (
	"errors"
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// GetExitRules returns the admission rules this node should install as an Exit.
func (h *NodeAgentHandler) GetExitRules(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)
	rules, err := services.ExitRulesForNode(c.UserContext(), h.db, nodeID)
	if err != nil {
		return policyRulesError(c, err, "exit")
	}
	return c.JSON(rules)
}

func policyRulesError(c *fiber.Ctx, err error, kind string) error {
	var invalid *services.InvalidPolicyRuleError
	if errors.As(err, &invalid) {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": invalid.Error()})
	}
	log.Printf("node %s %s rules: %v", c.Locals("node_id"), kind, err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("failed to fetch %s rules", kind)})
}
