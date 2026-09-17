package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

type AdminInboundHandler struct {
	db *pgxpool.Pool
}

func NewAdminInboundHandler(db *pgxpool.Pool) *AdminInboundHandler {
	return &AdminInboundHandler{db: db}
}

var allowedProtocols = map[string]bool{
	"vless_reality": true,
	"vmess_ws":      true,
	"trojan_tls":    true,
	"shadowsocks":   true,
	"hysteria2":     true,
	"wireguard":     true,
}

type createInboundRequest struct {
	Protocol string                 `json:"protocol"`
	Port     int                    `json:"port"`
	Tag      string                 `json:"tag"`
	Settings map[string]interface{} `json:"settings"`
	Enabled  *bool                  `json:"enabled"`
}

type updateInboundRequest struct {
	Protocol *string                `json:"protocol"`
	Port     *int                   `json:"port"`
	Tag      *string                `json:"tag"`
	Settings map[string]interface{} `json:"settings"`
	Enabled  *bool                  `json:"enabled"`
}

type inboundResponse struct {
	ID        string                 `json:"id"`
	NodeID    string                 `json:"node_id"`
	Protocol  string                 `json:"protocol"`
	Port      int                    `json:"port"`
	Tag       string                 `json:"tag"`
	Settings  map[string]interface{} `json:"settings"`
	Enabled   bool                   `json:"enabled"`
	CreatedAt time.Time              `json:"created_at"`
}

// List returns all inbounds for a node.
// @Summary List inbounds
// @Description Returns all inbounds for a given node
// @Tags admin-inbounds
// @Produce json
// @Param nodeId path string true "Node ID"
// @Success 200 {array} inboundResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{nodeId}/inbounds [get]
func (h *AdminInboundHandler) List(c *fiber.Ctx) error {
	nodeID := c.Params("nodeId")

	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, node_id, protocol, port, tag, settings, enabled, created_at
		 FROM inbounds WHERE node_id = $1 ORDER BY created_at`,
		nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list inbounds",
		})
	}
	defer rows.Close()

	inbounds := make([]inboundResponse, 0)
	for rows.Next() {
		var ib inboundResponse
		var settingsRaw json.RawMessage
		if err := rows.Scan(
			&ib.ID, &ib.NodeID, &ib.Protocol, &ib.Port, &ib.Tag,
			&settingsRaw, &ib.Enabled, &ib.CreatedAt,
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan inbound",
			})
		}
		if err := json.Unmarshal(settingsRaw, &ib.Settings); err != nil {
			ib.Settings = make(map[string]interface{})
		}
		inbounds = append(inbounds, ib)
	}

	return c.JSON(inbounds)
}

// Create adds a new inbound for a given node.
// @Summary Create inbound
// @Description Adds a new inbound configuration for a node
// @Tags admin-inbounds
// @Accept json
// @Produce json
// @Param nodeId path string true "Node ID"
// @Param body body createInboundRequest true "Inbound configuration"
// @Success 201 {object} inboundResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{nodeId}/inbounds [post]
func (h *AdminInboundHandler) Create(c *fiber.Ctx) error {
	nodeID := c.Params("nodeId")

	var req createInboundRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if !allowedProtocols[req.Protocol] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid protocol, must be one of: vless_reality, vmess_ws, trojan_tls, shadowsocks, hysteria2, wireguard",
		})
	}

	if req.Port <= 0 || req.Port >= 65536 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "port must be between 1 and 65535",
		})
	}

	if strings.TrimSpace(req.Tag) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "tag is required",
		})
	}

	// A node serves one protocol. Xray takes a single config per node, and
	// mixing protocols let a speed-limited user reach an uncapped inbound.
	var existing string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT protocol FROM inbounds WHERE node_id = $1 LIMIT 1`,
		nodeID,
	).Scan(&existing)
	if err == nil {
		msg := fmt.Sprintf("this node already serves %s; a node can only run one protocol", existing)
		if existing == req.Protocol {
			msg = fmt.Sprintf("this node already has a %s inbound; edit it instead of adding another", existing)
		}
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to check existing inbounds",
		})
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	settings := req.Settings
	if settings == nil {
		settings = make(map[string]interface{})
	}
	if req.Protocol == "wireguard" {
		if err := ensureWireGuardServerSettings(settings); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to generate wireguard server keys",
			})
		}
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to marshal settings",
		})
	}

	var ib inboundResponse
	var settingsRaw json.RawMessage
	err = h.db.QueryRow(
		context.Background(),
		`INSERT INTO inbounds (node_id, protocol, port, tag, settings, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, node_id, protocol, port, tag, settings, enabled, created_at`,
		nodeID, req.Protocol, req.Port, req.Tag, settingsJSON, enabled,
	).Scan(&ib.ID, &ib.NodeID, &ib.Protocol, &ib.Port, &ib.Tag, &settingsRaw, &ib.Enabled, &ib.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "port already in use on this node",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create inbound",
		})
	}

	if err := json.Unmarshal(settingsRaw, &ib.Settings); err != nil {
		ib.Settings = make(map[string]interface{})
	}

	return c.Status(fiber.StatusCreated).JSON(ib)
}

// Update partially updates an inbound by ID.
// @Summary Update inbound
// @Description Partially updates an inbound configuration
// @Tags admin-inbounds
// @Accept json
// @Produce json
// @Param id path string true "Inbound ID"
// @Param body body updateInboundRequest true "Fields to update"
// @Success 200 {object} inboundResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Security BearerAuth
// @Router /admin/inbounds/{id} [put]
func (h *AdminInboundHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updateInboundRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Protocol != nil && !allowedProtocols[*req.Protocol] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid protocol, must be one of: vless_reality, vmess_ws, trojan_tls, shadowsocks, hysteria2, wireguard",
		})
	}

	if req.Port != nil && (*req.Port <= 0 || *req.Port >= 65536) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "port must be between 1 and 65535",
		})
	}

	// Same one-protocol-per-node rule as Create: switching this inbound's
	// protocol must not leave its node serving two.
	if req.Protocol != nil {
		var conflicting string
		err := h.db.QueryRow(
			context.Background(),
			`SELECT other.protocol
			 FROM inbounds other
			 JOIN inbounds self ON self.node_id = other.node_id
			 WHERE self.id = $1 AND other.id <> $1
			 LIMIT 1`,
			id,
		).Scan(&conflicting)
		if err == nil && conflicting != *req.Protocol {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": fmt.Sprintf("this node already serves %s; a node can only run one protocol", conflicting),
			})
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to check existing inbounds",
			})
		}
	}

	// Build dynamic update query
	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Protocol != nil {
		setClauses = append(setClauses, fmt.Sprintf("protocol = $%d", argIdx))
		args = append(args, *req.Protocol)
		argIdx++
	}
	if req.Port != nil {
		setClauses = append(setClauses, fmt.Sprintf("port = $%d", argIdx))
		args = append(args, *req.Port)
		argIdx++
	}
	if req.Tag != nil {
		setClauses = append(setClauses, fmt.Sprintf("tag = $%d", argIdx))
		args = append(args, *req.Tag)
		argIdx++
	}
	if req.Settings != nil {
		protocol := ""
		if req.Protocol != nil {
			protocol = *req.Protocol
		} else {
			_ = h.db.QueryRow(context.Background(), `SELECT protocol FROM inbounds WHERE id = $1`, id).Scan(&protocol)
		}
		if protocol == "wireguard" {
			if pk, ok := req.Settings["private_key"]; !ok || pk == "" {
				// Preserve the existing server private key across unrelated
				// edits (e.g. toggling the port) so already-provisioned peers
				// don't silently lose their server identity.
				var existingRaw json.RawMessage
				if err := h.db.QueryRow(context.Background(), `SELECT settings FROM inbounds WHERE id = $1`, id).Scan(&existingRaw); err == nil {
					var existing map[string]interface{}
					if json.Unmarshal(existingRaw, &existing) == nil {
						if existingPK, ok := existing["private_key"]; ok {
							req.Settings["private_key"] = existingPK
						}
					}
				}
			}
			if err := ensureWireGuardServerSettings(req.Settings); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "failed to generate wireguard server keys",
				})
			}
		}

		settingsJSON, err := json.Marshal(req.Settings)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to marshal settings",
			})
		}
		setClauses = append(setClauses, fmt.Sprintf("settings = $%d", argIdx))
		args = append(args, settingsJSON)
		argIdx++
	}
	if req.Enabled != nil {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argIdx))
		args = append(args, *req.Enabled)
		argIdx++
	}

	if len(setClauses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no fields to update",
		})
	}

	query := fmt.Sprintf(
		`UPDATE inbounds SET %s WHERE id = $%d
		 RETURNING id, node_id, protocol, port, tag, settings, enabled, created_at`,
		strings.Join(setClauses, ", "), argIdx,
	)
	args = append(args, id)

	var ib inboundResponse
	var settingsRaw json.RawMessage
	err := h.db.QueryRow(context.Background(), query, args...).Scan(
		&ib.ID, &ib.NodeID, &ib.Protocol, &ib.Port, &ib.Tag, &settingsRaw, &ib.Enabled, &ib.CreatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "port already in use on this node",
			})
		}
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "inbound not found",
		})
	}

	if err := json.Unmarshal(settingsRaw, &ib.Settings); err != nil {
		ib.Settings = make(map[string]interface{})
	}

	return c.JSON(ib)
}

// Delete removes an inbound by ID.
// @Summary Delete inbound
// @Description Removes an inbound by ID
// @Tags admin-inbounds
// @Param id path string true "Inbound ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/inbounds/{id} [delete]
func (h *AdminInboundHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	result, err := h.db.Exec(
		context.Background(),
		`DELETE FROM inbounds WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete inbound",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "inbound not found",
		})
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// Toggle toggles the enabled state of an inbound.
// @Summary Toggle inbound
// @Description Toggles the enabled/disabled state of an inbound
// @Tags admin-inbounds
// @Produce json
// @Param id path string true "Inbound ID"
// @Success 200 {object} inboundResponse
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/inbounds/{id}/toggle [put]
func (h *AdminInboundHandler) Toggle(c *fiber.Ctx) error {
	id := c.Params("id")

	var ib inboundResponse
	var settingsRaw json.RawMessage
	err := h.db.QueryRow(
		context.Background(),
		`UPDATE inbounds SET enabled = NOT enabled WHERE id = $1
		 RETURNING id, node_id, protocol, port, tag, settings, enabled, created_at`,
		id,
	).Scan(&ib.ID, &ib.NodeID, &ib.Protocol, &ib.Port, &ib.Tag, &settingsRaw, &ib.Enabled, &ib.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "inbound not found",
		})
	}

	if err := json.Unmarshal(settingsRaw, &ib.Settings); err != nil {
		ib.Settings = make(map[string]interface{})
	}

	return c.JSON(ib)
}

// ensureWireGuardServerSettings fills in a wireguard inbound's server
// private_key and address if the admin didn't supply them, so the node-agent
// (see node-agent/cmd/main.go:buildWireGuardConfig) always has a usable
// interface config. address defaults to the whole wgAddressPoolBase pool so
// every device address allocated in user_device.go routes correctly back
// through this node's wg0 interface, regardless of which node it targets.
func ensureWireGuardServerSettings(settings map[string]interface{}) error {
	if pk, ok := settings["private_key"]; !ok || pk == "" {
		priv, _, err := crypto.GenerateWireGuardKeypair()
		if err != nil {
			return err
		}
		settings["private_key"] = priv
	}
	if addr, ok := settings["address"]; !ok || addr == "" {
		settings["address"] = fmt.Sprintf("%s.0.1/16", wgAddressPoolBase)
	}
	return nil
}
