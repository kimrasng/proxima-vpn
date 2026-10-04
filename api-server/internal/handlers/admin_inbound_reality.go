package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func (h *AdminInboundHandler) lockInboundRealityNode(ctx context.Context, nodeID string) (pgx.Tx, *services.LockedRealityNode, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin inbound mutation: %w", err)
	}
	locked, err := services.LockRealityNode(ctx, tx, nodeID)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return tx, locked, nil
}

func (h *AdminInboundHandler) lockExistingInbound(ctx context.Context, id string) (pgx.Tx, *services.LockedRealityNode, bool, error) {
	var nodeID string
	if err := h.db.QueryRow(ctx, `SELECT node_id::text FROM inbounds WHERE id=$1`, id).Scan(&nodeID); err != nil {
		return nil, nil, false, err
	}
	tx, locked, err := h.lockInboundRealityNode(ctx, nodeID)
	if err != nil {
		return nil, nil, false, err
	}
	var currentNode, protocol string
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT node_id::text, protocol, enabled FROM inbounds WHERE id=$1 FOR UPDATE`, id).Scan(&currentNode, &protocol, &enabled); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, false, err
	}
	if currentNode != nodeID {
		_ = tx.Rollback(ctx)
		return nil, nil, false, pgx.ErrNoRows
	}
	return tx, locked, enabled && protocol == "vless_reality", nil
}

func validateInboundReality(ctx context.Context, tx pgx.Tx, locked *services.LockedRealityNode, required bool) error {
	listeners, err := services.LoadEffectiveRealityListeners(ctx, tx, locked)
	if err != nil {
		return err
	}
	if len(listeners.Listeners) == 0 && !required {
		return services.MarkRealitySNINotApplicable(ctx, tx, locked)
	}
	return services.ValidateProspectiveRealityListeners(ctx, tx, locked, listeners)
}

func inboundRealityError(c *fiber.Ctx, err error, operation string) error {
	var realityError *services.RealitySNIError
	var pgError *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inbound not found"})
	case errors.As(err, &realityError):
		switch realityError.Kind {
		case services.RealitySNIMissingNode:
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node not found"})
		case services.RealitySNINoListener, services.RealitySNIListenerConflict, services.RealitySNIMismatch, services.RealitySNIInvalidListener, services.RealitySNIMalformed:
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Reality listeners are incompatible with the node SNI"})
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to " + operation + " inbound"})
		}
	case errors.As(err, &pgError) && pgError.Code == "23505":
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "port already in use on this node"})
	case errors.As(err, &pgError) && pgError.Code == "22P02":
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid inbound or node ID"})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to " + operation + " inbound"})
	}
}
