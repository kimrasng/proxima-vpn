package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminNodeGroupHandler handles admin node group CRUD endpoints.
type AdminNodeGroupHandler struct {
	db *pgxpool.Pool
}

// NewAdminNodeGroupHandler creates a new AdminNodeGroupHandler.
func NewAdminNodeGroupHandler(db *pgxpool.Pool) *AdminNodeGroupHandler {
	return &AdminNodeGroupHandler{db: db}
}

type createNodeGroupRequest struct {
	Name string `json:"name"`
}

type nodeGroupListItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	NodeCount int       `json:"node_count"`
	CreatedAt time.Time `json:"created_at"`
}

type nodeGroupDetail struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Nodes     []nodeGroupNode `json:"nodes"`
	CreatedAt time.Time       `json:"created_at"`
}

type nodeGroupNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type setNodesRequest struct {
	NodeIDs *[]string `json:"node_ids"`
}

// Create creates a new node group.
// @Summary Create node group
// @Description Creates a new node group
// @Tags admin-node-groups
// @Accept json
// @Produce json
// @Param body body createNodeGroupRequest true "Node group name"
// @Success 201 {object} nodeGroupListItem
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-groups [post]
func (h *AdminNodeGroupHandler) Create(c *fiber.Ctx) error {
	var req createNodeGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name is required",
		})
	}

	var group nodeGroupListItem
	err := h.db.QueryRow(
		context.Background(),
		`INSERT INTO node_groups (name) VALUES ($1)
		 RETURNING id, name, created_at`,
		req.Name,
	).Scan(&group.ID, &group.Name, &group.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create node group",
		})
	}

	group.NodeCount = 0
	return c.Status(fiber.StatusCreated).JSON(group)
}

// List returns all node groups with node counts.
// @Summary List node groups
// @Description Returns all node groups with their node counts
// @Tags admin-node-groups
// @Produce json
// @Success 200 {array} nodeGroupListItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-groups [get]
func (h *AdminNodeGroupHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT ng.id, ng.name, COUNT(ngn.node_id) AS node_count, ng.created_at
		 FROM node_groups ng
		 LEFT JOIN node_group_nodes ngn ON ng.id = ngn.node_group_id
		 GROUP BY ng.id, ng.name, ng.created_at
		 ORDER BY ng.created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list node groups",
		})
	}
	defer rows.Close()

	groups := make([]nodeGroupListItem, 0)
	for rows.Next() {
		var g nodeGroupListItem
		if err := rows.Scan(&g.ID, &g.Name, &g.NodeCount, &g.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan node group",
			})
		}
		groups = append(groups, g)
	}

	return c.JSON(groups)
}

// Get returns a single node group with its nodes.
// @Summary Get node group
// @Description Returns a single node group with its associated nodes
// @Tags admin-node-groups
// @Produce json
// @Param id path string true "Node Group ID"
// @Success 200 {object} nodeGroupDetail
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-groups/{id} [get]
func (h *AdminNodeGroupHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")

	var detail nodeGroupDetail
	err := h.db.QueryRow(
		context.Background(),
		`SELECT id, name, created_at FROM node_groups WHERE id = $1`,
		id,
	).Scan(&detail.ID, &detail.Name, &detail.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node group not found",
		})
	}

	rows, err := h.db.Query(
		context.Background(),
		`SELECT n.id, n.name, n.status
		 FROM nodes n
		 INNER JOIN node_group_nodes ngn ON n.id = ngn.node_id
		 WHERE ngn.node_group_id = $1
		 ORDER BY n.name`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch nodes",
		})
	}
	defer rows.Close()

	detail.Nodes = make([]nodeGroupNode, 0)
	for rows.Next() {
		var n nodeGroupNode
		if err := rows.Scan(&n.ID, &n.Name, &n.Status); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan node",
			})
		}
		detail.Nodes = append(detail.Nodes, n)
	}

	return c.JSON(detail)
}

// Update updates a node group's name.
// @Summary Update node group
// @Description Updates a node group's name
// @Tags admin-node-groups
// @Accept json
// @Produce json
// @Param id path string true "Node Group ID"
// @Param body body createNodeGroupRequest true "New name"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-groups/{id} [put]
func (h *AdminNodeGroupHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req createNodeGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name is required",
		})
	}

	tag, err := h.db.Exec(
		context.Background(),
		`UPDATE node_groups SET name = $1 WHERE id = $2`,
		req.Name, id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update node group",
		})
	}

	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node group not found",
		})
	}

	return c.JSON(fiber.Map{
		"id":   id,
		"name": req.Name,
	})
}

// Delete deletes a node group (CASCADE removes node_group_nodes).
// @Summary Delete node group
// @Description Deletes a node group and its node associations
// @Tags admin-node-groups
// @Param id path string true "Node Group ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-groups/{id} [delete]
func (h *AdminNodeGroupHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	tag, err := h.db.Exec(
		context.Background(),
		`DELETE FROM node_groups WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete node group",
		})
	}

	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node group not found",
		})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// SetNodes replaces all nodes in a group with the provided node IDs.
// @Summary Set nodes in group
// @Description Replaces all nodes in a group with the provided node IDs
// @Tags admin-node-groups
// @Accept json
// @Produce json
// @Param id path string true "Node Group ID"
// @Param body body setNodesRequest true "Node IDs to assign"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/node-groups/{id}/nodes [put]
func (h *AdminNodeGroupHandler) SetNodes(c *fiber.Ctx) error {
	id := c.Params("id")

	var req setNodesRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.NodeIDs == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "node_ids is required",
		})
	}
	nodeIDs := *req.NodeIDs
	ctx := context.Background()

	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to start transaction",
		})
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lockedGroupID string
	err = tx.QueryRow(ctx,
		`SELECT id::text FROM node_groups WHERE id = $1 FOR UPDATE`, id,
	).Scan(&lockedGroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node group not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to lock node group",
		})
	}

	rows, err := tx.Query(ctx,
		`SELECT n.id::text, n.role, n.id::text = ANY($1::text[])
		 FROM nodes n
		 WHERE n.id::text = ANY($1::text[])
		    OR EXISTS (
		      SELECT 1 FROM node_group_nodes ngn
		      WHERE ngn.node_group_id = $2 AND ngn.node_id = n.id
		    )
		 ORDER BY n.id
		 FOR UPDATE`,
		nodeIDs, id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to validate nodes",
		})
	}
	selectedNodes := 0
	forwardingNodes := 0
	for rows.Next() {
		var nodeID, role string
		var selected bool
		if err := rows.Scan(&nodeID, &role, &selected); err != nil {
			rows.Close()
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to validate nodes",
			})
		}
		if selected {
			selectedNodes++
			if role == "relay" || role == "both" {
				forwardingNodes++
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to validate nodes",
		})
	}
	if selectedNodes != len(nodeIDs) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "node_ids contains an unknown or duplicate node",
		})
	}
	var referenced bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM node_chains WHERE relay_pool_id = $1)`, id,
	).Scan(&referenced); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to validate relay pool",
		})
	}
	if referenced && (selectedNodes == 0 || forwardingNodes != selectedNodes) {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "referenced relay pool must remain nonempty and forwarding-only",
		})
	}

	_, err = tx.Exec(
		ctx,
		`DELETE FROM node_group_nodes WHERE node_group_id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to clear existing nodes",
		})
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id)
		 SELECT $1, node_id::uuid FROM unnest($2::text[]) AS node_id`,
		id, nodeIDs,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to add nodes",
		})
	}

	// Subscriptions are served from node_group_chains, so a membership edit that
	// touched only node_group_nodes would not reach a single client until the next
	// API restart re-ran the migration backfill. Every node gets a direct chain
	// created here if it does not have one, for the same reason the backfill
	// exists: a node with no chain is invisible.
	//
	// Relayed chains are left alone. They are placed deliberately, by pool, and
	// re-deriving them from node membership would delete an operator's work.
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO node_chains (name, relay_pool_id, exit_node_id, exit_port, transport)
		 SELECT n.name, NULL, n.id, n.port, 'tcp_udp'
		 FROM nodes n
		 WHERE n.id = ANY($1::uuid[])
		   AND NOT EXISTS (
		     SELECT 1 FROM node_chains c
		     WHERE c.exit_node_id = n.id AND c.relay_pool_id IS NULL AND c.entry_node_id IS NULL
		   )`,
		nodeIDs,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create node chains",
		})
	}

	if _, err := tx.Exec(
		ctx,
		`DELETE FROM node_group_chains ngc
		 USING node_chains c
		 WHERE ngc.chain_id = c.id
		   AND ngc.node_group_id = $1
		   AND c.relay_pool_id IS NULL AND c.entry_node_id IS NULL
		   AND NOT (c.exit_node_id = ANY($2::uuid[]))`,
		id, nodeIDs,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to clear group chains",
		})
	}

	if _, err := tx.Exec(
		ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id)
		 SELECT $1, c.id
		 FROM node_chains c
		 WHERE c.relay_pool_id IS NULL AND c.entry_node_id IS NULL AND c.exit_node_id = ANY($2::uuid[])
		 ON CONFLICT DO NOTHING`,
		id, nodeIDs,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to attach group chains",
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to commit transaction",
		})
	}

	return c.JSON(fiber.Map{
		"node_group_id": id,
		"node_ids":      nodeIDs,
	})
}
