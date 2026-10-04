package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

const (
	entryPortConflictMessage   = "entry port overlaps an existing chain"
	directChainConflictMessage = "an exit node already has a direct chain"
)

func lockedRelayPoolMemberCounts(ctx context.Context, query nodeChainRowsQuerier, poolID string) (int, int, error) {
	rows, err := query.Query(ctx,
		`SELECT n.role
		 FROM node_group_nodes ngn
		 JOIN nodes n ON n.id = ngn.node_id
		 WHERE ngn.node_group_id = $1
		 ORDER BY n.id
		 FOR UPDATE OF n`,
		poolID,
	)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	members := 0
	forwarders := 0
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return 0, 0, err
		}
		members++
		if role == "relay" || role == "both" {
			forwarders++
		}
	}
	return members, forwarders, rows.Err()
}

type createNodeChainRequest struct {
	Name        string  `json:"name"`
	EntryNodeID *string `json:"entry_node_id"`
	RelayPoolID *string `json:"relay_pool_id"`
	EntryHost   string  `json:"entry_host"`
	EntryPort   int     `json:"entry_port"`
	ExitNodeID  string  `json:"exit_node_id"`
	ExitPort    int     `json:"exit_port"`
	Transport   string  `json:"transport"`
	Priority    int     `json:"priority"`
}

// Create adds a chain.
// @Summary Create node chain
// @Tags admin-node-chains
// @Accept json
// @Produce json
// @Param body body createNodeChainRequest true "Chain definition"
// @Success 201 {object} nodeChainResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-chains [post]
func (h *AdminNodeChainHandler) Create(c *fiber.Ctx) error {
	var req createNodeChainRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	transport, err := validateCreateNodeChain(&req)
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return sendRouteManagementError(c, fiber.NewError(500, "failed to start transaction"))
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockChainCreation(ctx, tx, []createNodeChainRequest{req}); err != nil {
		return sendRouteManagementError(c, err)
	}
	chain, err := createNodeChainInTx(ctx, tx, req, transport)
	if err != nil {
		return sendRouteManagementError(c, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sendRouteManagementError(c, routeManagementDBError(err, "failed to commit transaction"))
	}
	h.logCreatedChain(ctx, c, chain)
	return c.Status(fiber.StatusCreated).JSON(chain)
}

func validateCreateNodeChain(req *createNodeChainRequest) (nodeprov.Transport, error) {
	if req.ExitNodeID == "" {
		return "", fiber.NewError(400, "exit_node_id is required")
	}
	if req.RelayPoolID != nil {
		return "", fiber.NewError(400, "relay_pool_id is no longer accepted; use entry_node_id")
	}
	req.EntryHost = strings.TrimSpace(req.EntryHost)
	if req.EntryNodeID == nil && (req.EntryHost != "" || req.EntryPort != 0) {
		return "", fiber.NewError(400, "direct chains cannot have an entry endpoint")
	}
	if req.EntryNodeID != nil && *req.EntryNodeID == req.ExitNodeID {
		return "", fiber.NewError(400, "entry and exit nodes must differ")
	}

	transport := nodeprov.Transport(req.Transport)
	if req.Transport == "" {
		transport = nodeprov.TransportTCP
	} else if !transport.Valid() {
		return "", fiber.NewError(400, fmt.Sprintf("unknown transport %q", req.Transport))
	}

	var err error
	req.ExitNodeID, err = normalizeRouteID(req.ExitNodeID, "exit_node_id")
	if err != nil {
		return "", err
	}
	if req.EntryNodeID != nil {
		id, err := normalizeRouteID(*req.EntryNodeID, "entry_node_id")
		if err != nil {
			return "", err
		}
		req.EntryNodeID = &id
		if id == req.ExitNodeID {
			return "", fiber.NewError(400, "entry and exit nodes must differ")
		}
	}
	if strings.ContainsRune(req.Name, 0) {
		return "", fiber.NewError(400, "name must not contain null characters")
	}
	for _, port := range []int{req.EntryPort, req.ExitPort} {
		if port != 0 {
			if err := nodeprov.ValidatePort(port); err != nil {
				return "", fiber.NewError(400, err.Error())
			}
		}
	}
	return transport, nil
}

// Both single and batch creation hold linked nodes in UUID order before calling
// this helper. Claims are fleet-wide; earlier inserts in this transaction are
// included, and the database's TCP/UDP unique indexes remain the final guard.
func createNodeChainInTx(ctx context.Context, tx pgx.Tx, req createNodeChainRequest, transport nodeprov.Transport) (nodeChainResponse, error) {
	var err error
	if req.EntryNodeID != nil {
		var entryRole, entryIP, entryStatus string
		err = tx.QueryRow(ctx,
			`SELECT role, host(ip), status FROM nodes WHERE id = $1`, *req.EntryNodeID,
		).Scan(&entryRole, &entryIP, &entryStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return nodeChainResponse{}, fiber.NewError(404, "entry node not found")
		}
		if err != nil {
			return nodeChainResponse{}, routeManagementDBError(err, "failed to validate entry node")
		}
		address, addressErr := netip.ParseAddr(entryIP)
		if entryStatus == "pending" || !nodeprov.Role(entryRole).Forwards() || addressErr != nil || !address.Is4() || address.IsUnspecified() {
			return nodeChainResponse{}, fiber.NewError(400, "entry node must forward and have an IPv4 address")
		}
	}

	var exitName, exitRole, exitStatus string
	var exitPort int
	err = tx.QueryRow(ctx,
		`SELECT name, port, role, status FROM nodes WHERE id = $1`, req.ExitNodeID,
	).Scan(&exitName, &exitPort, &exitRole, &exitStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nodeChainResponse{}, fiber.NewError(404, "exit node not found")
	}
	if err != nil {
		return nodeChainResponse{}, routeManagementDBError(err, "failed to validate exit node")
	}
	if !nodeprov.Role(exitRole).Exits() {
		return nodeChainResponse{}, fiber.NewError(400, "exit node has role relay and terminates no tunnels")
	}
	if req.EntryNodeID != nil && exitStatus == "pending" {
		return nodeChainResponse{}, fiber.NewError(400, "exit node must be registered")
	}
	entryHost := ""
	if req.EntryNodeID != nil {
		entryHost, err = services.ManagedEntryHostname(ctx, tx, *req.EntryNodeID)
		if errors.Is(err, services.ErrManagedEntryUnavailable) {
			return nodeChainResponse{}, fiber.NewError(409, "managed Entry hostname unavailable")
		}
		if err != nil {
			return nodeChainResponse{}, routeManagementDBError(err, "failed to read managed Entry hostname")
		}
		if req.EntryHost != "" && req.EntryHost != entryHost {
			return nodeChainResponse{}, fiber.NewError(400, "entry_host does not match managed Entry hostname")
		}
	}
	if req.ExitPort != 0 {
		exitPort = req.ExitPort
	}
	if err := nodeprov.ValidatePort(exitPort); err != nil {
		return nodeChainResponse{}, fiber.NewError(400, err.Error())
	}

	name := req.Name
	if name == "" {
		name = exitName
	}
	var entryPort *int
	if req.EntryNodeID != nil {
		claims, err := claimedEntryPorts(ctx, tx)
		if err != nil {
			return nodeChainResponse{}, routeManagementDBError(err, "failed to read claimed entry ports")
		}
		allocatedPort, err := nodeprov.AllocateEntryPort(req.EntryPort, transport, claims)
		if err != nil {
			if entryPortOverlaps(req.EntryPort, transport, claims) {
				return nodeChainResponse{}, fiber.NewError(409, entryPortConflictMessage)
			}
			if req.EntryPort != 0 {
				return nodeChainResponse{}, fiber.NewError(400, "invalid entry port")
			}
			return nodeChainResponse{}, fiber.NewError(409, "no entry ports available")
		}
		entryPort = &allocatedPort
	}

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO node_chains
		   (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport, priority)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text`,
		name, req.EntryNodeID, entryHost, entryPort, req.ExitNodeID, exitPort, string(transport), req.Priority,
	).Scan(&id)
	if err != nil {
		return nodeChainResponse{}, routeManagementDBError(err, "failed to create node chain")
	}

	chain, err := scanChain(tx.QueryRow(ctx, chainSelect+` WHERE c.id = $1`, id))
	if err != nil {
		return nodeChainResponse{}, routeManagementDBError(err, "failed to read created node chain")
	}
	return chain, nil
}

func (h *AdminNodeChainHandler) logCreatedChain(ctx context.Context, c *fiber.Ctx, chain nodeChainResponse) {
	h.activity.Log(ctx, services.Record{
		EventType: services.EventNodeUpdated, Severity: services.SeverityInfo,
		ActorType: "admin", ActorID: adminIDOf(c), ActorLabel: adminEmail(c),
		TargetType: "node", TargetID: chain.ExitNodeID,
		Detail: map[string]any{"chain": chain.Name, "transport": chain.Transport, "relayed": chain.EntryPort != nil},
	})
}
