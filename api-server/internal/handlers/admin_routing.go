package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services/ruleset"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
)

// AdminRoutingHandler edits the Clash Meta routing profile every subscription
// is rendered with.
type AdminRoutingHandler struct {
	db      *pgxpool.Pool
	catalog *ruleset.Catalog
	fetcher *ruleset.Fetcher
}

func NewAdminRoutingHandler(db *pgxpool.Pool) *AdminRoutingHandler {
	return &AdminRoutingHandler{db: db, catalog: ruleset.NewCatalog(), fetcher: ruleset.NewFetcher()}
}

type routingProfileResponse struct {
	Profile   subscription.RoutingProfile `json:"profile"`
	IsDefault bool                        `json:"is_default"`
	UpdatedAt *time.Time                  `json:"updated_at"`
}

type routingValidationResponse struct {
	Error  string                    `json:"error"`
	Fields []subscription.FieldError `json:"fields"`
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadRoutingProfile returns the stored profile, or the built-in default when
// none is stored. A stored document that no longer validates (for example
// written by a newer build) also falls back to the default rather than serving
// a profile clients would reject.
func loadRoutingProfile(ctx context.Context, db rowQuerier) (subscription.RoutingProfile, bool, *time.Time, error) {
	var raw []byte
	var updatedAt time.Time
	err := db.QueryRow(ctx, `SELECT document, updated_at FROM clash_routing_profile WHERE id = 1`).Scan(&raw, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return subscription.DefaultRoutingProfile(), true, nil, nil
	}
	if err != nil {
		return subscription.RoutingProfile{}, false, nil, err
	}
	var profile subscription.RoutingProfile
	if err := json.Unmarshal(raw, &profile); err != nil || len(subscription.ValidateRoutingProfile(profile)) != 0 {
		return subscription.DefaultRoutingProfile(), true, nil, nil
	}
	return profile, false, &updatedAt, nil
}

// parseRoutingProfile decodes and validates the body. When ok is false the
// 400 response has already been written and err is its write error.
func parseRoutingProfile(c *fiber.Ctx) (profile subscription.RoutingProfile, ok bool, err error) {
	if err := json.Unmarshal(c.Body(), &profile); err != nil {
		return profile, false, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if errs := subscription.ValidateRoutingProfile(profile); len(errs) != 0 {
		return profile, false, c.Status(fiber.StatusBadRequest).JSON(routingValidationResponse{Error: "invalid routing profile", Fields: errs})
	}
	return profile, true, nil
}

func (h *AdminRoutingHandler) respond(c *fiber.Ctx) error {
	profile, isDefault, updatedAt, err := loadRoutingProfile(c.UserContext(), h.db)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load routing profile"})
	}
	return c.JSON(routingProfileResponse{Profile: profile, IsDefault: isDefault, UpdatedAt: updatedAt})
}

// Get returns the routing profile subscriptions are rendered with.
// @Summary Get Clash routing profile
// @Tags admin-routing
// @Produce json
// @Success 200 {object} routingProfileResponse
// @Security BearerAuth
// @Router /admin/routing-profile [get]
func (h *AdminRoutingHandler) Get(c *fiber.Ctx) error {
	return h.respond(c)
}

// Update validates and stores a routing profile.
// @Summary Replace Clash routing profile
// @Tags admin-routing
// @Accept json
// @Produce json
// @Param body body subscription.RoutingProfile true "Routing profile"
// @Success 200 {object} routingProfileResponse
// @Failure 400 {object} routingValidationResponse
// @Security BearerAuth
// @Router /admin/routing-profile [put]
func (h *AdminRoutingHandler) Update(c *fiber.Ctx) error {
	profile, ok, err := parseRoutingProfile(c)
	if !ok {
		return err
	}
	doc, err := json.Marshal(profile)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to encode routing profile"})
	}
	if _, err := h.db.Exec(c.UserContext(),
		`INSERT INTO clash_routing_profile (id, document, updated_at) VALUES (1, $1, NOW())
		 ON CONFLICT (id) DO UPDATE SET document = EXCLUDED.document, updated_at = NOW()`, doc); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to save routing profile"})
	}
	return h.respond(c)
}

// Reset drops the stored profile so the built-in default is served again.
// @Summary Reset Clash routing profile to default
// @Tags admin-routing
// @Produce json
// @Success 200 {object} routingProfileResponse
// @Security BearerAuth
// @Router /admin/routing-profile [delete]
func (h *AdminRoutingHandler) Reset(c *fiber.Ctx) error {
	if _, err := h.db.Exec(c.UserContext(), `DELETE FROM clash_routing_profile WHERE id = 1`); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to reset routing profile"})
	}
	return h.respond(c)
}

// Preview renders a profile against the published exits without storing it.
// Proxies carry placeholder addresses and credentials, so the YAML is safe to
// show; only names, countries and network types come from real nodes.
// @Summary Preview Clash routing profile
// @Tags admin-routing
// @Accept json
// @Produce json
// @Param body body subscription.RoutingProfile true "Routing profile"
// @Success 200 {object} map[string]string
// @Failure 400 {object} routingValidationResponse
// @Security BearerAuth
// @Router /admin/routing-profile/preview [post]
func (h *AdminRoutingHandler) Preview(c *fiber.Ctx) error {
	profile, ok, err := parseRoutingProfile(c)
	if !ok {
		return err
	}
	nodes, err := h.previewNodes(c.UserContext())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load nodes"})
	}
	out, err := subscription.GenerateRoutedClash(nodes, "00000000-0000-0000-0000-000000000000", nil, profile)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to render preview"})
	}
	return c.JSON(fiber.Map{"yaml": string(out)})
}

func (h *AdminRoutingHandler) previewNodes(ctx context.Context) ([]subscription.NodeInfo, error) {
	rows, err := h.db.Query(ctx,
		`SELECT id::text, name, tags FROM nodes
		 WHERE status != 'pending' AND role IN ('exit','both')
		 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []subscription.NodeInfo
	for rows.Next() {
		var id, name string
		var tags []string
		if err := rows.Scan(&id, &name, &tags); err != nil {
			return nil, err
		}
		nodes = append(nodes, subscription.NodeInfo{
			Name: name, IP: "192.0.2.1", Port: 443, Protocol: "trojan_tls", TLSEnabled: true,
			ServerName: "preview.invalid", NodeID: id, Tags: tags,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		nodes = []subscription.NodeInfo{{Name: "example node", IP: "192.0.2.1", Port: 443, Protocol: "trojan_tls"}}
	}
	return nodes, nil
}

type nodeTagCount struct {
	Tag   string `json:"tag"`
	Nodes int    `json:"nodes"`
}

// NodeTags lists every tag in use with the number of nodes carrying it, for
// the tag pickers in the routing and node editors.
// @Summary List node tags
// @Tags admin-routing
// @Produce json
// @Success 200 {array} nodeTagCount
// @Security BearerAuth
// @Router /admin/node-tags [get]
func (h *AdminRoutingHandler) NodeTags(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(),
		`SELECT tag, count(*)::int FROM nodes, unnest(tags) AS tag GROUP BY tag ORDER BY tag`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list tags"})
	}
	defer rows.Close()
	out := make([]nodeTagCount, 0)
	for rows.Next() {
		var t nodeTagCount
		if err := rows.Scan(&t.Tag, &t.Nodes); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read tags"})
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read tags"})
	}
	return c.JSON(out)
}

// RuleSetCatalog lists the MetaCubeX meta-rules-dat geosite/geoip rule sets.
// @Summary List MetaCubeX rule sets
// @Tags admin-routing
// @Produce json
// @Success 200 {array} ruleset.CatalogEntry
// @Failure 502 {object} map[string]string
// @Security BearerAuth
// @Router /admin/rule-sets/catalog [get]
func (h *AdminRoutingHandler) RuleSetCatalog(c *fiber.Ctx) error {
	entries, err := h.catalog.Entries(c.UserContext())
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "could not load the MetaCubeX catalog: " + err.Error()})
	}
	return c.JSON(entries)
}

// RuleSetContents downloads a rule set from jsDelivr/GitHub and returns its
// decoded entries (mrs, yaml or text).
// @Summary Show rule set contents
// @Tags admin-routing
// @Produce json
// @Param url query string true "Rule set URL (jsDelivr or GitHub)"
// @Param format query string false "mrs, yaml or text"
// @Success 200 {object} ruleset.Contents
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /admin/rule-sets/contents [get]
func (h *AdminRoutingHandler) RuleSetContents(c *fiber.Ctx) error {
	out, err := h.fetcher.Fetch(c.UserContext(), c.Query("url"), c.Query("format"))
	if err != nil {
		status := fiber.StatusBadGateway
		if errors.Is(err, ruleset.ErrHostNotAllowed) {
			status = fiber.StatusBadRequest
		}
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(out)
}

// DecodeRuleSet decodes an uploaded rule-set file, for the mrs viewer.
// @Summary Decode an uploaded rule set file
// @Tags admin-routing
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "Rule set file"
// @Success 200 {object} ruleset.Contents
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /admin/rule-sets/decode [post]
func (h *AdminRoutingHandler) DecodeRuleSet(c *fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "file is required"})
	}
	if fh.Size > 32<<20 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "file is larger than 32 MiB"})
	}
	f, err := fh.Open()
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cannot read file"})
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cannot read file"})
	}
	format := c.FormValue("format")
	if format == "" {
		format = "text"
		switch {
		case strings.HasSuffix(fh.Filename, ".mrs"):
			format = "mrs"
		case strings.HasSuffix(fh.Filename, ".yaml"), strings.HasSuffix(fh.Filename, ".yml"):
			format = "yaml"
		}
	}
	out, err := ruleset.Decode(data, format)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(out)
}
