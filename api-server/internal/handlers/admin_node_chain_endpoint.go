package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type updateNodeChainRequest struct {
	Name      *string `json:"name"`
	EntryHost *string `json:"entry_host"`
	Priority  *int    `json:"priority"`
	Enabled   *bool   `json:"enabled"`
}

// Update changes a chain's presentation and whether it is served. The addresses
// and ports are deliberately immutable: clients have them saved, so changing one
// silently breaks every config already issued - delete and recreate instead.
// @Summary Update node chain
// @Tags admin-node-chains
// @Accept json
// @Produce json
// @Param id path string true "Chain ID"
// @Param body body updateNodeChainRequest true "Fields to update"
// @Success 200 {object} nodeChainResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-chains/{id} [patch]
func (h *AdminNodeChainHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updateNodeChainRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == nil && req.EntryHost == nil && req.Priority == nil && req.Enabled == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no fields to update"})
	}
	if req.EntryHost != nil {
		trimmed := strings.TrimSpace(*req.EntryHost)
		req.EntryHost = &trimmed
	}

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to start transaction"})
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var relayPoolID, entryNodeID *string
	err = tx.QueryRow(ctx, `SELECT relay_pool_id::text, entry_node_id::text FROM node_chains WHERE id = $1`, id).Scan(&relayPoolID, &entryNodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node chain not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read node chain"})
	}
	if relayPoolID != nil || entryNodeID != nil {
		if req.EntryHost != nil && *req.EntryHost == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "entry_host is required for a relayed chain"})
		}
	}
	if relayPoolID != nil {
		var lockedPoolID string
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM node_groups WHERE id = $1 FOR UPDATE`, *relayPoolID,
		).Scan(&lockedPoolID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to lock relay pool"})
		}
		members, forwarders, err := lockedRelayPoolMemberCounts(ctx, tx, *relayPoolID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to validate relay pool"})
		}
		if members == 0 || members != forwarders {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "relay pool must remain nonempty and forwarding-only"})
		}
	}
	var lockedChainID string
	if err := tx.QueryRow(ctx,
		`SELECT id::text FROM node_chains WHERE id = $1 FOR UPDATE`, id,
	).Scan(&lockedChainID); errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node chain not found"})
	} else if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to lock node chain"})
	}

	if entryNodeID != nil && req.EntryHost != nil {
		hostname, err := services.ManagedEntryHostname(ctx, tx, *entryNodeID)
		if errors.Is(err, services.ErrManagedEntryUnavailable) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "managed Entry hostname unavailable"})
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read managed Entry hostname"})
		}
		if *req.EntryHost != hostname {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "entry_host does not match managed Entry hostname"})
		}
	}
	updateHost := req.EntryHost
	if entryNodeID != nil {
		updateHost = nil
	}

	_, err = tx.Exec(ctx,
		`UPDATE node_chains SET
		   name       = COALESCE($1, name),
		   entry_host = COALESCE($2, entry_host),
		   priority   = COALESCE($3, priority),
		   enabled    = COALESCE($4, enabled)
		 WHERE id = $5`,
		req.Name, updateHost, req.Priority, req.Enabled, id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update node chain",
		})
	}

	ch, err := scanChain(tx.QueryRow(ctx, chainSelect+` WHERE c.id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node chain not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read node chain",
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to commit transaction"})
	}

	return c.JSON(ch)
}
