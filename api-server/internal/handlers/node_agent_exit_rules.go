package handlers

import (
	"context"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func (h *NodeAgentHandler) GetExitRules(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)
	rows, err := h.db.Query(
		context.Background(),
		`SELECT c.exit_port, c.transport,
		        ARRAY(
		          SELECT host(relay.ip)
		          FROM (
		            SELECT DISTINCT n.ip
		            FROM nodes n
		            WHERE (n.id = c.entry_node_id OR (c.entry_node_id IS NULL AND EXISTS (
		              SELECT 1 FROM node_group_nodes ngn
		              WHERE ngn.node_group_id = c.relay_pool_id AND ngn.node_id = n.id
		            )))
		              AND n.role IN ('relay', 'both')
		              AND family(n.ip) = 4
		            ORDER BY n.ip
		          ) relay
		        )
		 FROM node_chains c
		 WHERE c.exit_node_id = $1
		   AND (c.entry_node_id IS NOT NULL OR c.relay_pool_id IS NOT NULL)
		   AND c.enabled = true
		   AND c.mode = 'l4_dnat'
		 ORDER BY c.exit_port, c.transport, c.id`,
		nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch exit rules",
		})
	}
	defer rows.Close()

	rules := make([]nodeprov.ExitRule, 0)
	for rows.Next() {
		var rule nodeprov.ExitRule
		if err := rows.Scan(&rule.ExitPort, &rule.Transport, &rule.RelayIPs); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan exit rule",
			})
		}
		if err := rule.Validate(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fmt.Sprintf("invalid exit rule: %v", err),
			})
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read exit rules",
		})
	}

	nodeprov.SortExitRules(rules)
	return c.JSON(rules)
}
