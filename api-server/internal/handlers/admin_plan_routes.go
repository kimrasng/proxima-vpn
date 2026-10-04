package handlers

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

type planRoutesRequest struct {
	ChainIDs *[]string `json:"chain_ids"`
}

type planRoutesResponse struct {
	ChainIDs         []string `json:"chain_ids"`
	NodeGroupID      string   `json:"node_group_id"`
	SpeedLimit       *int     `json:"speed_limit,omitempty"`
	SpeedEnforcement string   `json:"speed_enforcement"`
	Warnings         []string `json:"warnings"`
}

type routePlan struct {
	ID          string
	NodeGroupID string
}

func lockRoutePlans(ctx context.Context, tx pgx.Tx, ids []string) ([]routePlan, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, node_group_id::text FROM plans WHERE id = ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE`, ids)
	if err != nil {
		return nil, routeManagementDBError(err, "failed to lock plans")
	}
	defer rows.Close()
	plans := make([]routePlan, 0, len(ids))
	for rows.Next() {
		var plan routePlan
		if err := rows.Scan(&plan.ID, &plan.NodeGroupID); err != nil {
			return nil, routeManagementDBError(err, "failed to read plans")
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, routeManagementDBError(err, "failed to read plans")
	}
	if len(plans) != len(ids) {
		return nil, fiber.NewError(404, "plan not found")
	}
	return plans, nil
}

func lockRouteGroups(ctx context.Context, tx pgx.Tx, ids []string) error {
	rows, err := tx.Query(ctx, `SELECT id::text FROM node_groups WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return routeManagementDBError(err, "failed to lock route groups")
	}
	defer rows.Close()
	found := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return routeManagementDBError(err, "failed to read route groups")
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return routeManagementDBError(err, "failed to read route groups")
	}
	for _, id := range ids {
		if !found[id] {
			return fiber.NewError(400, "group_ids contains an unknown group")
		}
	}
	return nil
}

// The plan row and original group must be locked first. The group FOR UPDATE
// lock blocks concurrent FK references, so the shared check cannot race a new
// plan assignment. Copy both projections: chains control subscription access,
// nodes control exit client provisioning. A relay pool is never mutated into
// an exit-access group, even when only one plan references it.
func isolatePlanRouteGroup(ctx context.Context, tx pgx.Tx, plan routePlan) (string, error) {
	var shared, relayPool bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM plans WHERE node_group_id = $1 AND id <> $2),
		        EXISTS(SELECT 1 FROM node_chains WHERE relay_pool_id = $1)`, plan.NodeGroupID, plan.ID,
	).Scan(&shared, &relayPool); err != nil {
		return "", routeManagementDBError(err, "failed to check shared route group")
	}
	if !shared && !relayPool {
		return plan.NodeGroupID, nil
	}
	var groupID string
	if err := tx.QueryRow(ctx, `INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, "plan-routes-"+crypto.NewUUID()).Scan(&groupID); err != nil {
		return "", routeManagementDBError(err, "failed to isolate plan route group")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id)
		 SELECT $1, chain_id FROM node_group_chains WHERE node_group_id = $2`, groupID, plan.NodeGroupID,
	); err != nil {
		return "", routeManagementDBError(err, "failed to copy plan route access")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id)
		 SELECT $1, node_id FROM node_group_nodes WHERE node_group_id = $2`, groupID, plan.NodeGroupID,
	); err != nil {
		return "", routeManagementDBError(err, "failed to copy plan exit access")
	}
	if _, err := tx.Exec(ctx, `UPDATE plans SET node_group_id = $1 WHERE id = $2`, groupID, plan.ID); err != nil {
		return "", routeManagementDBError(err, "failed to isolate plan access")
	}
	return groupID, nil
}

func readPlanRoutes(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, planID string) (planRoutesResponse, error) {
	var result planRoutesResponse
	err := query.QueryRow(ctx,
		`SELECT p.node_group_id::text, p.speed_limit,
		 ARRAY(SELECT ngc.chain_id::text FROM node_group_chains ngc WHERE ngc.node_group_id = p.node_group_id ORDER BY ngc.chain_id)
		 FROM plans p WHERE p.id = $1`, planID,
	).Scan(&result.NodeGroupID, &result.SpeedLimit, &result.ChainIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, fiber.NewError(404, "plan not found")
	}
	if err != nil {
		return result, routeManagementDBError(err, "failed to read plan routes")
	}
	result.SpeedEnforcement = "unlimited"
	result.Warnings = []string{"route_assignment_not_subscriber_readiness"}
	if result.SpeedLimit != nil && *result.SpeedLimit > 0 {
		result.SpeedEnforcement = "device_global_v1"
		result.Warnings = append(result.Warnings, "device_bandwidth_requires_current_agent_ack", "vless_reality_only", "payload_rate_not_wire_rate")
	}
	return result, nil
}

// GetRoutes reports assignments, not the subscriber-specific subscription
// projection. Per-device policy still requires acknowledged compatible agents.
// @Summary Get plan route assignments
// @Tags admin-plans
// @Produce json
// @Param id path string true "Plan ID"
// @Success 200 {object} planRoutesResponse
// @Security BearerAuth
// @Router /admin/plans/{id}/routes [get]
func (h *AdminPlanHandler) GetRoutes(c *fiber.Ctx) error {
	id, err := normalizeRouteID(c.Params("id"), "plan id")
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	result, err := readPlanRoutes(context.Background(), h.db, id)
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	return c.JSON(result)
}

// SetRoutes replaces only this plan's access, isolating a shared group first.
// @Summary Set plan route assignments
// @Tags admin-plans
// @Accept json
// @Produce json
// @Param id path string true "Plan ID"
// @Param body body planRoutesRequest true "Selected route IDs"
// @Success 200 {object} planRoutesResponse
// @Security BearerAuth
// @Router /admin/plans/{id}/routes [put]
func (h *AdminPlanHandler) SetRoutes(c *fiber.Ctx) error {
	id, err := normalizeRouteID(c.Params("id"), "plan id")
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	var req planRoutesRequest
	if err := c.BodyParser(&req); err != nil {
		return sendRouteManagementError(c, fiber.NewError(400, "invalid request body"))
	}
	if req.ChainIDs == nil {
		return sendRouteManagementError(c, fiber.NewError(400, "chain_ids is required"))
	}
	chainIDs, err := normalizeRouteIDs(*req.ChainIDs, "chain_ids")
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to start transaction"))
	}
	defer func() { _ = tx.Rollback(ctx) }()
	plans, err := lockRoutePlans(ctx, tx, []string{id})
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	if err := lockRouteGroups(ctx, tx, []string{plans[0].NodeGroupID}); err != nil {
		return sendRouteManagementError(c, err)
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM node_chains WHERE id = ANY($1::uuid[]) ORDER BY id FOR KEY SHARE`, chainIDs)
	if err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to validate routes"))
	}
	found := 0
	for rows.Next() {
		var chainID string
		if err := rows.Scan(&chainID); err != nil {
			rows.Close()
			return sendRouteManagementError(c, routeManagementDBError(err, "failed to read routes"))
		}
		found++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to read routes"))
	}
	if found != len(chainIDs) {
		return sendRouteManagementError(c, fiber.NewError(400, "chain_ids contains an unknown route"))
	}
	groupID, err := isolatePlanRouteGroup(ctx, tx, plans[0])
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM node_group_chains WHERE node_group_id = $1`, groupID); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to clear plan routes"))
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id) SELECT $1, chain_id FROM unnest($2::uuid[]) AS chain_id`, groupID, chainIDs,
	); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to assign plan routes"))
	}
	// Keep the provisioning projection exact without granting any unselected
	// direct chain: access derives from selected exits, not SetNodes' backfill.
	if _, err := tx.Exec(ctx, `DELETE FROM node_group_nodes WHERE node_group_id = $1`, groupID); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to clear plan exit access"))
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id)
		 SELECT DISTINCT $1::uuid, exit_node_id FROM node_chains WHERE id = ANY($2::uuid[])`, groupID, chainIDs,
	); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to assign plan exit access"))
	}
	result, err := readPlanRoutes(ctx, tx, id)
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to commit transaction"))
	}
	return c.JSON(result)
}
