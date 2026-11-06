package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type ConnectionHistoryHandler struct {
	service *services.ConnectionHistoryService
}

func NewConnectionHistoryHandler(service *services.ConnectionHistoryService) *ConnectionHistoryHandler {
	return &ConnectionHistoryHandler{service: service}
}

// RegisterRoutes accepts already-authenticated groups, matching the existing
// /api/v1/nodes/:id and /api/v1/admin groups. Identity guards fail closed even
// when a caller accidentally omits those groups' authentication middleware.
func (h *ConnectionHistoryHandler) RegisterRoutes(node, admin fiber.Router) {
	node.Get("/connection-history/config", h.Config)
	node.Post("/connection-history", h.Ingest)
	admin.Post("/nodes/connection-logging", h.BulkControl)
	admin.Get("/nodes/:id/connection-logging", h.Control)
	admin.Get("/connection-history/sessions", h.Sessions)
	admin.Get("/connection-history/flows", h.Flows)
	admin.Get("/connection-history/flows/:node_id/:collection_epoch/:entity_id", h.Flow)
	admin.Get("/connection-history/sessions/:node_id/:collection_epoch/:entity_id", h.Session)
	admin.Get("/connection-history/aggregate", h.Aggregate)
}
func historyNode(c *fiber.Ctx) (string, error) {
	node, ok := c.Locals("node_id").(string)
	if !ok || node == "" || node != c.Params("id") {
		return "", fiber.ErrUnauthorized
	}
	return node, nil
}
func historyAdmin(c *fiber.Ctx) error {
	id, ok := c.Locals("admin_id").(string)
	if !ok || id == "" {
		return fiber.ErrUnauthorized
	}
	return nil
}
func historyResponse(c *fiber.Ctx, value any, err error) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	if err == nil {
		return c.JSON(value)
	}
	var domain *services.ConnectionHistoryError
	if errors.As(err, &domain) {
		return c.Status(domain.Status).JSON(domain)
	}
	return c.Status(500).JSON(fiber.Map{"error": "connection_history_failed"})
}
func (h *ConnectionHistoryHandler) Config(c *fiber.Ctx) error {
	node, err := historyNode(c)
	if err != nil {
		return err
	}
	cfg, err := h.service.Config(c.UserContext(), node)
	return historyResponse(c, cfg, err)
}
func (h *ConnectionHistoryHandler) Ingest(c *fiber.Ctx) error {
	node, err := historyNode(c)
	if err != nil {
		return err
	}
	batch, err := services.DecodeConnectionHistoryBatch(c.Body())
	if err != nil {
		return historyResponse(c, nil, err)
	}
	ack, err := h.service.Ingest(c.UserContext(), node, batch)
	return historyResponse(c, ack, err)
}
func (h *ConnectionHistoryHandler) BulkControl(c *fiber.Ctx) error {
	if err := historyAdmin(c); err != nil {
		return err
	}
	if len(c.Body()) > services.ConnectionHistoryMaxBatchBytes {
		return c.SendStatus(413)
	}
	if _, err := nodeprov.CanonicalHistoryJSON(c.Body()); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_json"})
	}
	var cmd services.ConnectionHistoryBulkCommand
	var members map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &members); err != nil {
		return c.SendStatus(400)
	}
	if members["enabled"] == nil || members["nodes"] == nil || bytes.Equal(members["enabled"], []byte("null")) {
		return c.SendStatus(400)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(members["nodes"], &nodes); err != nil {
		return c.SendStatus(400)
	}
	for _, node := range nodes {
		if node["id"] == nil || node["expected_revision"] == nil {
			return c.SendStatus(400)
		}
	}
	d := json.NewDecoder(bytes.NewReader(c.Body()))
	d.DisallowUnknownFields()
	if err := d.Decode(&cmd); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_command"})
	}
	configs, err := h.service.BulkControl(c.UserContext(), cmd)
	// Never return private manifests to admin APIs.
	result := map[string]any{}
	for id, cfg := range configs {
		result[id] = fiber.Map{"enabled": cfg.Enabled, "revision": cfg.Revision, "collection_epoch": cfg.CollectionEpoch}
	}
	return historyResponse(c, result, err)
}
func (h *ConnectionHistoryHandler) Control(c *fiber.Ctx) error {
	if err := historyAdmin(c); err != nil {
		return err
	}
	if nodeprov.HistoryUUID(c.Params("id")).Validate() != nil {
		return c.SendStatus(400)
	}
	out, err := h.service.ReadControl(c.UserContext(), c.Params("id"))
	return historyResponse(c, out, err)
}
func historyQuery(c *fiber.Ctx) (services.ConnectionHistoryQuery, error) {
	q := services.ConnectionHistoryQuery{NodeID: c.Query("node_id"), UserID: c.Query("user_id"), Epoch: c.Query("collection_epoch"), TunnelID: c.Query("tunnel_id"), EntityID: c.Query("entity_id"), Cursor: c.Query("cursor"), Limit: c.QueryInt("limit", 50)}
	q.Destination = c.Query("destination")
	q.DestinationPrefix = c.Query("destination_prefix")
	q.Protocol = c.Query("protocol")
	q.Status = c.Query("status")
	for _, v := range []struct {
		key    string
		target **time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if raw := c.Query(v.key); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return q, fiber.ErrBadRequest
			}
			*v.target = &t
		}
	}
	return q, nil
}
func (h *ConnectionHistoryHandler) query(c *fiber.Ctx, flows bool) error {
	if err := historyAdmin(c); err != nil {
		return err
	}
	q, err := historyQuery(c)
	if err != nil {
		return err
	}
	page, err := h.service.Query(c.UserContext(), flows, q)
	return historyResponse(c, page, err)
}
func (h *ConnectionHistoryHandler) Sessions(c *fiber.Ctx) error { return h.query(c, false) }
func (h *ConnectionHistoryHandler) Flows(c *fiber.Ctx) error    { return h.query(c, true) }
func (h *ConnectionHistoryHandler) detail(c *fiber.Ctx, flows bool) error {
	if err := historyAdmin(c); err != nil {
		return err
	}
	entity, err := url.PathUnescape(c.Params("entity_id"))
	if err != nil {
		return fiber.ErrBadRequest
	}
	row, err := h.service.Detail(c.UserContext(), flows, c.Params("node_id"), c.Params("collection_epoch"), entity)
	return historyResponse(c, row, err)
}
func (h *ConnectionHistoryHandler) Flow(c *fiber.Ctx) error    { return h.detail(c, true) }
func (h *ConnectionHistoryHandler) Session(c *fiber.Ctx) error { return h.detail(c, false) }
func (h *ConnectionHistoryHandler) Aggregate(c *fiber.Ctx) error {
	if err := historyAdmin(c); err != nil {
		return err
	}
	q, err := historyQuery(c)
	if err != nil {
		return err
	}
	totals, err := h.service.Aggregate(c.UserContext(), q)
	return historyResponse(c, totals, err)
}
