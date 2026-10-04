package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// AdminNodeChainHandler handles the client-facing endpoints an operator composes:
// each chain is one entry a subscription lists, pointing at an exit either
// directly or through a relay pool.
type AdminNodeChainHandler struct {
	db       *pgxpool.Pool
	activity *services.ActivityService
}

func NewAdminNodeChainHandler(db *pgxpool.Pool) *AdminNodeChainHandler {
	return &AdminNodeChainHandler{db: db, activity: services.NewActivityService(db)}
}

type nodeChainResponse struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	RelayPoolID   *string    `json:"relay_pool_id,omitempty"`
	RelayPoolName *string    `json:"relay_pool_name,omitempty"`
	EntryNodeID   *string    `json:"entry_node_id,omitempty"`
	EntryNodeName *string    `json:"entry_node_name,omitempty"`
	EntryHost     string     `json:"entry_host"`
	EntryPort     *int       `json:"entry_port,omitempty"`
	ExitNodeID    string     `json:"exit_node_id"`
	ExitNodeName  string     `json:"exit_node_name"`
	ExitPort      int        `json:"exit_port"`
	Transport     string     `json:"transport"`
	Mode          string     `json:"mode"`
	Priority      int        `json:"priority"`
	Enabled       bool       `json:"enabled"`
	Health        string     `json:"health"`
	LastProbeAt   *time.Time `json:"last_probe_at,omitempty"`
	ProbeRTTMs    *int       `json:"probe_rtt_ms,omitempty"`
	GroupIDs      []string   `json:"group_ids"`
	CreatedAt     time.Time  `json:"created_at"`
}

const chainSelect = `
	SELECT c.id::text, c.name, c.relay_pool_id::text, pool.name, c.entry_node_id::text, entry.name, c.entry_host, c.entry_port,
	       c.exit_node_id::text, n.name, c.exit_port, c.transport, c.mode,
	       c.priority, c.enabled, c.health, c.last_probe_at, c.probe_rtt_ms, c.created_at,
	       ARRAY(SELECT ngc.node_group_id::text FROM node_group_chains ngc
	             WHERE ngc.chain_id = c.id ORDER BY ngc.node_group_id)
	FROM node_chains c
	JOIN nodes n ON n.id = c.exit_node_id
	LEFT JOIN nodes entry ON entry.id = c.entry_node_id
	LEFT JOIN node_groups pool ON pool.id = c.relay_pool_id`

func scanChain(row pgx.Row) (nodeChainResponse, error) {
	var ch nodeChainResponse
	err := row.Scan(
		&ch.ID, &ch.Name, &ch.RelayPoolID, &ch.RelayPoolName, &ch.EntryNodeID, &ch.EntryNodeName, &ch.EntryHost, &ch.EntryPort,
		&ch.ExitNodeID, &ch.ExitNodeName, &ch.ExitPort, &ch.Transport, &ch.Mode,
		&ch.Priority, &ch.Enabled, &ch.Health, &ch.LastProbeAt, &ch.ProbeRTTMs, &ch.CreatedAt, &ch.GroupIDs,
	)
	return ch, err
}

// List returns every chain, newest first.
// @Summary List node chains
// @Tags admin-node-chains
// @Produce json
// @Success 200 {array} nodeChainResponse
// @Security BearerAuth
// @Router /admin/node-chains [get]
func (h *AdminNodeChainHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(context.Background(), chainSelect+` ORDER BY c.created_at DESC`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list node chains",
		})
	}
	defer rows.Close()

	chains := make([]nodeChainResponse, 0)
	for rows.Next() {
		ch, err := scanChain(rows)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan node chain",
			})
		}
		chains = append(chains, ch)
	}
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read node chains",
		})
	}

	return c.JSON(chains)
}

// Delete removes a chain, freeing its entry port.
// @Summary Delete node chain
// @Tags admin-node-chains
// @Param id path string true "Chain ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-chains/{id} [delete]
func (h *AdminNodeChainHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to start transaction"})
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var relayed bool
	err = tx.QueryRow(ctx,
		`SELECT relay_pool_id IS NOT NULL OR entry_node_id IS NOT NULL FROM node_chains WHERE id = $1 FOR UPDATE`, id,
	).Scan(&relayed)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node chain not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to lock node chain",
		})
	}
	if !relayed {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "direct node chains cannot be deleted"})
	}
	if _, err := tx.Exec(ctx, `DELETE FROM node_chains WHERE id = $1`, id); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete node chain"})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to commit transaction"})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// SetGroups replaces the plans' node groups a chain is served to, which is what
// decides who sees it.
// @Summary Set node chain groups
// @Tags admin-node-chains
// @Accept json
// @Produce json
// @Param id path string true "Chain ID"
// @Param body body setChainGroupsRequest true "Group IDs"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-chains/{id}/groups [put]
func (h *AdminNodeChainHandler) SetGroups(c *fiber.Ctx) error {
	id := c.Params("id")

	var req setChainGroupsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.GroupIDs == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "group_ids is required"})
	}
	groupIDs := *req.GroupIDs

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to start transaction",
		})
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Access writers lock groups in UUID order before chains, matching plan
	// route replacement and batch group isolation. Include removed groups so
	// replacing an assignment also serializes with their plan access writers.
	rows, err := tx.Query(ctx,
		`SELECT g.id::text, g.id::text = ANY($1::text[])
		 FROM node_groups g
		 WHERE g.id::text = ANY($1::text[])
		    OR EXISTS (SELECT 1 FROM node_group_chains ngc WHERE ngc.node_group_id = g.id AND ngc.chain_id::text = $2)
		 ORDER BY g.id FOR UPDATE`,
		groupIDs, id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to validate groups",
		})
	}
	validatedGroups := 0
	lockedGroups := make([]string, 0)
	for rows.Next() {
		var groupID string
		var selected bool
		if err := rows.Scan(&groupID, &selected); err != nil {
			rows.Close()
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to validate groups",
			})
		}
		lockedGroups = append(lockedGroups, groupID)
		if selected {
			validatedGroups++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to validate groups",
		})
	}
	if validatedGroups != len(groupIDs) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "group_ids contains an unknown or duplicate group",
		})
	}

	var lockedChainID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM node_chains WHERE id = $1 FOR UPDATE`, id).Scan(&lockedChainID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node chain not found"})
	}
	if err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to lock node chain"))
	}
	// Another assignment could have added a previously unassigned group while
	// we waited for the chain. Never acquire that group after the chain lock:
	// abort so a retry can take the complete group set in the proper order.
	var membershipChanged bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM node_group_chains WHERE chain_id = $1 AND NOT (node_group_id = ANY($2::uuid[])))`,
		id, lockedGroups,
	).Scan(&membershipChanged); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to validate chain groups"))
	}
	if membershipChanged {
		return sendRouteManagementError(c, fiber.NewError(409, "route assignment changed concurrently; retry the request"))
	}

	if _, err := tx.Exec(ctx, `DELETE FROM node_group_chains WHERE chain_id = $1`, id); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to clear chain groups",
		})
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id)
		 SELECT group_id::uuid, $1 FROM unnest($2::text[]) AS group_id`,
		id, groupIDs,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to replace chain groups",
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to commit transaction",
		})
	}

	return c.JSON(fiber.Map{"chain_id": id, "group_ids": groupIDs})
}

type setChainGroupsRequest struct {
	GroupIDs *[]string `json:"group_ids"`
}
