package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type batchNodeChainRoute struct {
	Name       string `json:"name"`
	ExitNodeID string `json:"exit_node_id"`
	ExitPort   int    `json:"exit_port"`
	EntryPort  int    `json:"entry_port"`
	Transport  string `json:"transport"`
}

type batchNodeChainsRequest struct {
	EntryNodeID string                `json:"entry_node_id"`
	Routes      []batchNodeChainRoute `json:"routes"`
	GroupIDs    []string              `json:"group_ids"`
	PlanIDs     []string              `json:"plan_ids"`
	Preview     bool                  `json:"preview"`
}

type batchNodeChainsResponse struct {
	Routes  []nodeChainResponse `json:"routes"`
	Preview bool                `json:"preview"`
}

func normalizeRouteID(raw, field string) (string, error) {
	var id pgtype.UUID
	if err := id.Scan(raw); err != nil || !id.Valid {
		return "", fiber.NewError(400, field+" must be a UUID")
	}
	value, err := id.Value()
	if err != nil {
		return "", fiber.NewError(400, field+" must be a UUID")
	}
	return value.(string), nil
}

func normalizeRouteIDs(ids []string, field string) ([]string, error) {
	result := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, raw := range ids {
		id, err := normalizeRouteID(raw, field)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fiber.NewError(400, field+" contains a duplicate ID")
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func routeManagementDBError(err error, message string) error {
	if isEntryPortConflict(err) {
		return fiber.NewError(409, entryPortConflictMessage)
	}
	if isDirectChainConflict(err) {
		return fiber.NewError(409, directChainConflictMessage)
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && (databaseError.Code == "40001" || databaseError.Code == "40P01" || databaseError.Code == "23503") {
		return fiber.NewError(409, "route assignment changed concurrently; retry the request")
	}
	return fiber.NewError(500, message)
}

func sendRouteManagementError(c *fiber.Ctx, err error) error {
	var clientError *fiber.Error
	if errors.As(err, &clientError) {
		return c.Status(clientError.Code).JSON(fiber.Map{"error": clientError.Message})
	}
	return c.Status(500).JSON(fiber.Map{"error": "route management failed"})
}

func lockChainCreation(ctx context.Context, tx pgx.Tx, requests []createNodeChainRequest) error {
	ids := make([]string, 0, len(requests)+1)
	relayed := false
	for _, req := range requests {
		ids = append(ids, req.ExitNodeID)
		if req.EntryNodeID != nil {
			ids = append(ids, *req.EntryNodeID)
			relayed = true
		}
	}
	if relayed {
		// Serialize automatic fleet-wide allocation across different Entries.
		// Unique TCP/UDP claim indexes also protect against other DB writers.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin_node_chain_entry_ports', 0))`); err != nil {
			return routeManagementDBError(err, "failed to lock entry port allocation")
		}
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM nodes WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return routeManagementDBError(err, "failed to lock linked nodes")
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return routeManagementDBError(err, "failed to lock linked nodes")
		}
	}
	if err := rows.Err(); err != nil {
		return routeManagementDBError(err, "failed to lock linked nodes")
	}
	return nil
}

func validateBatchNodeChains(req *batchNodeChainsRequest) ([]createNodeChainRequest, []nodeprov.Transport, error) {
	entryID, err := normalizeRouteID(req.EntryNodeID, "entry_node_id")
	if err != nil {
		return nil, nil, err
	}
	req.EntryNodeID = entryID
	if len(req.Routes) == 0 || len(req.Routes) > 100 {
		return nil, nil, fiber.NewError(400, "routes must contain between 1 and 100 routes")
	}
	req.GroupIDs, err = normalizeRouteIDs(req.GroupIDs, "group_ids")
	if err != nil {
		return nil, nil, err
	}
	req.PlanIDs, err = normalizeRouteIDs(req.PlanIDs, "plan_ids")
	if err != nil {
		return nil, nil, err
	}
	requests := make([]createNodeChainRequest, len(req.Routes))
	transports := make([]nodeprov.Transport, len(req.Routes))
	for i, route := range req.Routes {
		requests[i] = createNodeChainRequest{
			Name: route.Name, EntryNodeID: &entryID, ExitNodeID: route.ExitNodeID,
			ExitPort: route.ExitPort, EntryPort: route.EntryPort, Transport: route.Transport,
		}
		transports[i], err = validateCreateNodeChain(&requests[i])
		if err != nil {
			var clientError *fiber.Error
			if errors.As(err, &clientError) {
				return nil, nil, fiber.NewError(clientError.Code, fmt.Sprintf("routes[%d]: %s", i, clientError.Message))
			}
			return nil, nil, err
		}
	}
	return requests, transports, nil
}

// Batch creates every route and its access assignments in one transaction.
// Preview runs the same writes, including uniqueness checks, then rolls them
// back. Returned IDs are ephemeral and ports are NOT reserved: clients may
// resubmit the chosen explicit ports, but must handle a subsequent 409.
// @Summary Preview or create Entry routes atomically
// @Tags admin-node-chains
// @Accept json
// @Produce json
// @Param body body batchNodeChainsRequest true "Entry routes and access assignments"
// @Success 200 {object} batchNodeChainsResponse
// @Success 201 {object} batchNodeChainsResponse
// @Security BearerAuth
// @Router /admin/node-chains/batch [post]
func (h *AdminNodeChainHandler) Batch(c *fiber.Ctx) error {
	var req batchNodeChainsRequest
	if err := c.BodyParser(&req); err != nil {
		return sendRouteManagementError(c, fiber.NewError(400, "invalid request body"))
	}
	requests, transports, err := validateBatchNodeChains(&req)
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to start transaction"))
	}
	defer func() { _ = tx.Rollback(ctx) }()

	plans, err := lockRoutePlans(ctx, tx, req.PlanIDs)
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	groupIDs := append([]string{}, req.GroupIDs...)
	for _, plan := range plans {
		groupIDs = append(groupIDs, plan.NodeGroupID)
	}
	if err := lockRouteGroups(ctx, tx, groupIDs); err != nil {
		return sendRouteManagementError(c, err)
	}
	// Explicit access groups must not also be legacy forwarding pools: adding
	// exit memberships would violate their forwarding-only invariant.
	var forwardingGroup bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_chains WHERE relay_pool_id = ANY($1::uuid[]))`, req.GroupIDs).Scan(&forwardingGroup); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to validate access groups"))
	}
	if forwardingGroup {
		return sendRouteManagementError(c, fiber.NewError(400, "group_ids cannot contain a relay pool"))
	}
	for _, plan := range plans {
		groupID, err := isolatePlanRouteGroup(ctx, tx, plan)
		if err != nil {
			return sendRouteManagementError(c, err)
		}
		// Only explicit group_ids intentionally grant access to every plan in
		// a shared group. plan_ids never broadens another plan's access.
		req.GroupIDs = append(req.GroupIDs, groupID)
	}
	if err := lockChainCreation(ctx, tx, requests); err != nil {
		return sendRouteManagementError(c, err)
	}
	// Reserve explicit requests in-memory first, so an earlier automatic
	// route cannot steal a later route's requested port. Persist in request
	// order, and still let the unique claim indexes check every insertion.
	reserved, err := claimedEntryPorts(ctx, tx)
	if err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to read claimed entry ports"))
	}

	for i := range requests {
		if requests[i].EntryPort == 0 {
			continue
		}
		port, err := nodeprov.AllocateEntryPort(requests[i].EntryPort, transports[i], reserved)
		if err != nil {
			if entryPortOverlaps(requests[i].EntryPort, transports[i], reserved) {
				return sendRouteManagementError(c, fiber.NewError(409, entryPortConflictMessage))
			}
			return sendRouteManagementError(c, fiber.NewError(400, "invalid entry port"))
		}
		reserved = append(reserved, nodeprov.PortClaim{Port: port, Transport: transports[i]})
	}
	for i := range requests {
		if requests[i].EntryPort != 0 {
			continue
		}
		port, err := nodeprov.AllocateEntryPort(0, transports[i], reserved)
		if err != nil {
			return sendRouteManagementError(c, fiber.NewError(409, "no entry ports available"))
		}
		requests[i].EntryPort = port
		reserved = append(reserved, nodeprov.PortClaim{Port: port, Transport: transports[i]})
	}

	chains := make([]nodeChainResponse, 0, len(requests))
	for i, request := range requests {
		chain, err := createNodeChainInTx(ctx, tx, request, transports[i])
		if err != nil {
			return sendRouteManagementError(c, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO node_group_chains (node_group_id, chain_id)
			 SELECT DISTINCT group_id, $1::uuid FROM unnest($2::uuid[]) AS group_id
			 ON CONFLICT DO NOTHING`, chain.ID, req.GroupIDs,
		); err != nil {
			return sendRouteManagementError(c, routeManagementDBError(err, "failed to assign route groups"))
		}
		// Client provisioning reads node_group_nodes, independently of the
		// subscription's chain selection. Grant the selected exits as well.
		if _, err := tx.Exec(ctx,
			`INSERT INTO node_group_nodes (node_group_id, node_id)
			 SELECT DISTINCT group_id, $1::uuid FROM unnest($2::uuid[]) AS group_id
			 ON CONFLICT DO NOTHING`, chain.ExitNodeID, req.GroupIDs,
		); err != nil {
			return sendRouteManagementError(c, routeManagementDBError(err, "failed to assign exit access"))
		}
		chain, err = scanChain(tx.QueryRow(ctx, chainSelect+` WHERE c.id = $1`, chain.ID))
		if err != nil {
			return sendRouteManagementError(c, routeManagementDBError(err, "failed to read assigned route"))
		}
		chains = append(chains, chain)
	}
	status := fiber.StatusCreated
	if req.Preview {
		if err := tx.Rollback(ctx); err != nil {
			return sendRouteManagementError(c, routeManagementDBError(err, "failed to roll back preview"))
		}
		status = fiber.StatusOK
	} else {
		if err := tx.Commit(ctx); err != nil {
			return sendRouteManagementError(c, routeManagementDBError(err, "failed to commit transaction"))
		}
		for _, chain := range chains {
			h.logCreatedChain(ctx, c, chain)
		}
	}
	return c.Status(status).JSON(batchNodeChainsResponse{Routes: chains, Preview: req.Preview})
}
