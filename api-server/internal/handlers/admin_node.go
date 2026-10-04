package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/lang"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/proximavpn/proxima-vpn/pkg/xrayver"
	"github.com/redis/go-redis/v9"
)

// AdminNodeHandler handles admin node management endpoints.
type AdminNodeHandler struct {
	db       *pgxpool.Pool
	redis    *redis.Client
	panelURL string
	activity *services.ActivityService
}

// NewAdminNodeHandler creates a new AdminNodeHandler.
func NewAdminNodeHandler(db *pgxpool.Pool, rdb *redis.Client, panelURL string) *AdminNodeHandler {
	return &AdminNodeHandler{
		db:       db,
		redis:    rdb,
		panelURL: panelURL,
		activity: services.NewActivityService(db),
	}
}

const (
	xrayLatestVersionCacheKey = "xray:latest_version"
	xrayLatestVersionCacheTTL = 10 * time.Minute
	xrayReleasesURL           = "https://api.github.com/repos/XTLS/Xray-core/releases/latest"
)

// latestXrayVersion returns the latest published Xray-core release tag, caching
// the result in Redis to avoid hitting the GitHub API rate limit. On any error
// it returns an empty string so callers degrade gracefully.
func (h *AdminNodeHandler) latestXrayVersion(ctx context.Context) string {
	if h.redis != nil {
		if cached, err := h.redis.Get(ctx, xrayLatestVersionCacheKey).Result(); err == nil && cached != "" {
			return cached
		}
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, xrayReleasesURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}

	if payload.TagName != "" && h.redis != nil {
		h.redis.Set(ctx, xrayLatestVersionCacheKey, payload.TagName, xrayLatestVersionCacheTTL)
	}
	return payload.TagName
}

type generateTokenResponse struct {
	Token          string `json:"token"`
	InstallCommand string `json:"install_command"`
	NodeID         string `json:"node_id"`
	FirewallPorts  string `json:"firewall_ports"`
}

// provisionNodeRequest carries the operator's provisioning choices. Every field
// is optional so an empty body still yields a usable token.
type provisionNodeRequest struct {
	OSFamily           string              `json:"os_family"`
	Role               string              `json:"role"`
	Name               string              `json:"name"`
	Country            string              `json:"country"`
	Region             string              `json:"region"`
	Port               int                 `json:"port"`
	TrafficMultiplier  *float64            `json:"traffic_multiplier"`
	MaxConcurrentConns *int                `json:"max_concurrent_conns"`
	FirewallPreset     string              `json:"firewall_preset"`
	CustomPorts        []nodeprov.PortSpec `json:"custom_ports"`
}

// GenerateToken creates a one-time registration token for a new node.
// @Summary Generate node registration token
// @Description Records the supplied provisioning configuration against a pending node and returns a one-time registration token plus a tailored install command
// @Tags admin-nodes
// @Accept json
// @Produce json
// @Param body body provisionNodeRequest false "Provisioning configuration"
// @Success 200 {object} generateTokenResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/token [post]
func (h *AdminNodeHandler) GenerateToken(c *fiber.Ctx) error {
	var req provisionNodeRequest
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid request body",
			})
		}
	}

	osFamily := nodeprov.OSFamily(req.OSFamily)
	if req.OSFamily != "" && !osFamily.Valid() {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("unknown os_family %q", req.OSFamily),
		})
	}

	role := nodeprov.Role(req.Role)
	if req.Role == "" {
		role = nodeprov.DefaultRole
	} else if !role.Valid() {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("unknown role %q", req.Role),
		})
	}

	preset := nodeprov.FirewallPreset(req.FirewallPreset)
	if req.FirewallPreset == "" {
		preset = nodeprov.DefaultPreset
	} else if !preset.Valid() {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("unknown firewall_preset %q", req.FirewallPreset),
		})
	}

	port := req.Port
	if port == 0 {
		port = nodeprov.DefaultServicePort
	}

	specs, err := nodeprov.ResolvePorts(role, preset, port, req.CustomPorts)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	ports := nodeprov.FormatPorts(specs)

	multiplier := 1.0
	if req.TrafficMultiplier != nil {
		multiplier = *req.TrafficMultiplier
		if multiplier <= 0 || multiplier > maxTrafficMultiplier {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("traffic_multiplier must be greater than 0 and at most %g", maxTrafficMultiplier),
			})
		}
	}

	maxConns := 0
	if req.MaxConcurrentConns != nil {
		maxConns = *req.MaxConcurrentConns
		if maxConns < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "max_concurrent_conns cannot be negative",
			})
		}
	}

	// The operator finds a pending node in the list by name, so keep the marker
	// only when none was supplied.
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "pending"
	}

	token := crypto.GenerateRandomString(32)

	var nodeID string
	err = h.db.QueryRow(
		context.Background(),
		`INSERT INTO nodes (name, reg_token, api_key, country, region, ip, port, status,
		                    os_family, role, traffic_multiplier, max_concurrent_conns,
		                    firewall_preset, firewall_ports)
		 VALUES ($1, $2, 'pending', $3, $4, '0.0.0.0'::inet, $5, 'pending',
		         $6, $7, $8, $9, $10, $11)
		 RETURNING id::text`,
		name, token, req.Country, req.Region, port,
		string(osFamily), string(role), multiplier, maxConns, string(preset), ports,
	).Scan(&nodeID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to generate registration token",
		})
	}

	return c.JSON(generateTokenResponse{
		Token:          token,
		InstallCommand: h.buildInstallCommand(token, name, req.Country, req.Region, port, ports),
		NodeID:         nodeID,
		FirewallPorts:  ports,
	})
}

// buildInstallCommand renders the one-liner an operator pastes onto the node.
// Operator-supplied values are single-quoted and embedded quotes escaped: these
// reach a shell, so unquoted input would be a command injection.
func (h *AdminNodeHandler) buildInstallCommand(token, name, country, region string, port int, ports string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "bash <(curl -s %s/scripts/install.sh) --server %s --token %s",
		h.panelURL, h.panelURL, token)
	fmt.Fprintf(&b, " --port %d", port)
	if ports != "" {
		fmt.Fprintf(&b, " --ports %s", ports)
	}
	if name != "" && name != "pending" {
		fmt.Fprintf(&b, " --name %s", shellQuote(name))
	}
	if country != "" {
		fmt.Fprintf(&b, " --country %s", shellQuote(country))
	}
	if region != "" {
		fmt.Fprintf(&b, " --region %s", shellQuote(region))
	}
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// nodeEventFiltersResponse keeps the filter list server-side so the UI cannot drift.
type nodeEventFiltersResponse struct {
	EventTypes []string `json:"event_types"`
	Severities []string `json:"severities"`
}

// GetNodeEventFilters returns the event types and severities a node feed can contain.
// @Summary List node event filter options
// @Description Returns the event types and severities available for filtering node events
// @Tags admin-nodes
// @Produce json
// @Success 200 {object} nodeEventFiltersResponse
// @Security BearerAuth
// @Router /admin/nodes/event-filters [get]
func (h *AdminNodeHandler) GetNodeEventFilters(c *fiber.Ctx) error {
	return c.JSON(nodeEventFiltersResponse{
		EventTypes: services.NodeEventTypes(),
		Severities: []string{
			string(services.SeverityInfo),
			string(services.SeveritySuccess),
			string(services.SeverityWarning),
			string(services.SeverityError),
		},
	})
}

// GetNodeEvents returns a filtered, paged slice of one node's event history.
// @Summary Get node events
// @Description Returns one node's events newest first, filtered by type, severity and time range
// @Tags admin-nodes
// @Produce json
// @Param id path string true "Node ID"
// @Param event_type query string false "Comma-separated event types"
// @Param severity query string false "Comma-separated severities"
// @Param hours query int false "Only events within this many hours"
// @Param limit query int false "Page size (default 25, max 200)"
// @Param offset query int false "Rows to skip"
// @Success 200 {object} services.NodeEventPage
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id}/events [get]
func (h *AdminNodeHandler) GetNodeEvents(c *fiber.Ctx) error {
	query := services.NodeEventQuery{
		NodeID:     c.Params("id"),
		EventTypes: splitCSVParam(c.Query("event_type")),
		Severities: splitCSVParam(c.Query("severity")),
		Limit:      c.QueryInt("limit", 25),
		Offset:     c.QueryInt("offset", 0),
	}

	if hours := c.QueryInt("hours", 0); hours > 0 {
		since := time.Now().Add(-time.Duration(hours) * time.Hour)
		query.Since = &since
	}

	page, err := h.activity.ListNodeEvents(context.Background(), query)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch node events",
		})
	}
	return c.JSON(page)
}

// splitCSVParam drops blanks, so "a,,b" cannot yield a filter that matches nothing.
func splitCSVParam(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

type nodeMetricsEntry struct {
	CPUUsage    float64   `json:"cpu_usage"`
	MemoryUsage float64   `json:"memory_usage"`
	DiskUsage   float64   `json:"disk_usage"`
	LoadAvg     float64   `json:"load_avg"`
	NetworkIn   float64   `json:"network_in"`
	NetworkOut  float64   `json:"network_out"`
	RecordedAt  time.Time `json:"recorded_at"`
}

func (h *AdminNodeHandler) GetMetricsHistory(c *fiber.Ctx) error {
	id := c.Params("id")
	hours := c.QueryInt("hours", 24)
	if hours < 1 {
		hours = 1
	}
	if hours > 168 {
		hours = 168
	}

	rows, err := h.db.Query(
		context.Background(),
		`SELECT cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out, recorded_at
		 FROM node_metrics_history
		 WHERE node_id = $1 AND recorded_at >= NOW() - make_interval(hours => $2)
		 ORDER BY recorded_at ASC`,
		id, hours,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch metrics history",
		})
	}
	defer rows.Close()

	entries := make([]nodeMetricsEntry, 0)
	for rows.Next() {
		var e nodeMetricsEntry
		if err := rows.Scan(
			&e.CPUUsage, &e.MemoryUsage, &e.DiskUsage, &e.LoadAvg,
			&e.NetworkIn, &e.NetworkOut, &e.RecordedAt,
		); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	return c.JSON(entries)
}

type nodeListItem struct {
	nodeEndpointFields
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Country     string     `json:"country"`
	Region      string     `json:"region"`
	IP          string     `json:"ip"`
	Port        int        `json:"port"`
	Status      string     `json:"status"`
	XrayVersion string     `json:"xray_version"`
	CPUUsage    *float64   `json:"cpu_usage"`
	MemoryUsage *float64   `json:"memory_usage"`
	DiskUsage   *float64   `json:"disk_usage"`
	LoadAvg     *float64   `json:"load_avg"`
	NetworkIn   *float64   `json:"network_in"`
	NetworkOut  *float64   `json:"network_out"`
	LastSeen    *time.Time `json:"last_seen"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastPingAt  *time.Time `json:"last_ping_at"`
	// When status last flipped; null for nodes that predate the column.
	StatusChangedAt *time.Time `json:"status_changed_at"`
	// From the heartbeat: status only says the agent checked in, these say
	// whether Xray is actually serving the published config.
	XrayRunning *bool `json:"xray_running"`
	// Surfaces a node that cannot run tc: speed-limited plans on it are not
	// actually limited, and nothing else about the node looks wrong.
	ShapingOK    *bool   `json:"shaping_ok"`
	ShapingMode  string  `json:"shaping_mode"`
	ShapingTiers *int    `json:"shaping_tiers"`
	ShapingError *string `json:"shaping_error"`
	// Factor applied to this node's traffic when charging a user's quota.
	TrafficMultiplier float64 `json:"traffic_multiplier"`
	// Set when the node's core predates the stats RPC the panel needs. Reported
	// rather than refused: the node still carries traffic, and cutting it off
	// over a version would be worse than telling the operator to upgrade it.
	XrayTooOld         bool    `json:"xray_too_old"`
	XrayMinimum        string  `json:"xray_minimum"`
	XrayVersionWarning string  `json:"xray_version_warning,omitempty"`
	ConfigHash         *string `json:"config_hash"`
	// Device credentials live on this node now, against how many the plans
	// pointing at it are entitled to place. Capacity is an entitlement ceiling,
	// not a limit - nothing refuses a connection for exceeding it.
	OnlineDevices int `json:"online_devices"`
	Capacity      int `json:"capacity"`

	OSFamily           string `json:"os_family"`
	Role               string `json:"role"`
	PublishDirect      bool   `json:"publish_direct"`
	MaxConcurrentConns int    `json:"max_concurrent_conns"`
	FirewallPreset     string `json:"firewall_preset"`
	FirewallPorts      string `json:"firewall_ports"`

	// Per-language names keyed by language code, populated by GetNode only;
	// the list endpoint omits them rather than joining for every row.
	Labels map[string]string `json:"labels,omitempty"`
}

// ListNodes returns all nodes including pending ones.
// @Summary List nodes
// @Description Returns all nodes including pending (awaiting registration) ones
// @Tags admin-nodes
// @Produce json
// @Success 200 {array} nodeListItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes [get]
func (h *AdminNodeHandler) ListNodes(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT nodes.id, name, country, region, ip::text, port, status, xray_version,
		        cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out,
			        last_seen, nodes.created_at, nodes.updated_at, last_ping_at, status_changed_at,
		        xray_running, config_hash,
		        shaping_ok, shaping_tiers, shaping_error, traffic_multiplier, shaping_mode,
		        os_family, role, publish_direct, max_concurrent_conns, firewall_preset, firewall_ports`+
			nodeEndpointSelection+` ORDER BY nodes.created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list nodes",
		})
	}
	defer rows.Close()

	nodes := make([]nodeListItem, 0)
	for rows.Next() {
		var n nodeListItem
		targets := []any{
			&n.ID, &n.Name, &n.Country, &n.Region, &n.IP, &n.Port,
			&n.Status, &n.XrayVersion,
			&n.CPUUsage, &n.MemoryUsage, &n.DiskUsage, &n.LoadAvg, &n.NetworkIn, &n.NetworkOut,
			&n.LastSeen, &n.CreatedAt, &n.UpdatedAt, &n.LastPingAt, &n.StatusChangedAt,
			&n.XrayRunning, &n.ConfigHash,
			&n.ShapingOK, &n.ShapingTiers, &n.ShapingError, &n.TrafficMultiplier, &n.ShapingMode,
			&n.OSFamily, &n.Role, &n.PublishDirect, &n.MaxConcurrentConns, &n.FirewallPreset, &n.FirewallPorts,
		}
		if err := rows.Scan(append(targets, n.nodeEndpointFields.scanTargets()...)...); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan node",
			})
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list nodes"})
	}

	occupancy := h.nodeOccupancy(context.Background())
	for i := range nodes {
		annotateXrayVersion(&nodes[i])
		if o, ok := occupancy[nodes[i].ID]; ok {
			nodes[i].OnlineDevices = o.online
			nodes[i].Capacity = o.capacity
		}
	}
	return c.JSON(nodes)
}

type nodeOccupancy struct {
	online   int
	capacity int
}

// nodeOccupancy counts live credentials per node and the number the plans
// routed to it could place there. Live counts come from Redis via the tracker
// because the database has no notion of who is connected right now.
func (h *AdminNodeHandler) nodeOccupancy(ctx context.Context) map[string]nodeOccupancy {
	out := map[string]nodeOccupancy{}

	rows, err := h.db.Query(ctx, `
		SELECT ngn.node_id::text, COALESCE(SUM(sub.devices), 0)
		FROM node_group_nodes ngn
		LEFT JOIN (
			SELECT p.node_group_id, COUNT(d.id) AS devices
			FROM plans p
			JOIN users u ON u.plan_id = p.id
			JOIN devices d ON d.user_id = u.id
			WHERE u.status = 'active' AND u.is_active = true
			GROUP BY p.node_group_id
		) sub ON sub.node_group_id = ngn.node_group_id
		GROUP BY ngn.node_id
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var nodeID string
			var capacity int
			if err := rows.Scan(&nodeID, &capacity); err != nil {
				continue
			}
			out[nodeID] = nodeOccupancy{capacity: capacity}
		}
	}

	tracker := services.NewOnlineTracker(h.redis)
	byUUID, err := tracker.GetAllOnlineUUIDs(ctx)
	if err != nil {
		return out
	}
	for _, nodeID := range byUUID {
		entry := out[nodeID]
		entry.online++
		out[nodeID] = entry
	}
	return out
}

// annotateXrayVersion fills the version-floor verdict, which is derived rather
// than stored so raising the floor takes effect without a migration.
func annotateXrayVersion(n *nodeListItem) {
	n.XrayMinimum = xrayver.Minimum
	if xrayver.AtLeastMinimum(n.XrayVersion) {
		return
	}
	n.XrayTooOld = true
	n.XrayVersionWarning = xrayver.Explain(n.XrayVersion)
}

// GetNode returns a single node by ID.
// @Summary Get node
// @Description Returns a single node by ID
// @Tags admin-nodes
// @Produce json
// @Param id path string true "Node ID"
// @Success 200 {object} nodeListItem
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id} [get]
func (h *AdminNodeHandler) GetNode(c *fiber.Ctx) error {
	id := c.Params("id")

	var n nodeListItem
	row := h.db.QueryRow(
		context.Background(),
		`SELECT nodes.id, name, country, region, ip::text, port, status, xray_version,
		        cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out,
			        last_seen, nodes.created_at, nodes.updated_at, last_ping_at, status_changed_at,
		        xray_running, config_hash,
		        shaping_ok, shaping_tiers, shaping_error, traffic_multiplier, shaping_mode,
		        os_family, role, publish_direct, max_concurrent_conns, firewall_preset, firewall_ports`+
			nodeEndpointSelection+` WHERE nodes.id = $1`,
		id,
	)
	targets := []any{
		&n.ID, &n.Name, &n.Country, &n.Region, &n.IP, &n.Port,
		&n.Status, &n.XrayVersion,
		&n.CPUUsage, &n.MemoryUsage, &n.DiskUsage, &n.LoadAvg, &n.NetworkIn, &n.NetworkOut,
		&n.LastSeen, &n.CreatedAt, &n.UpdatedAt, &n.LastPingAt, &n.StatusChangedAt,
		&n.XrayRunning, &n.ConfigHash,
		&n.ShapingOK, &n.ShapingTiers, &n.ShapingError, &n.TrafficMultiplier, &n.ShapingMode,
		&n.OSFamily, &n.Role, &n.PublishDirect, &n.MaxConcurrentConns, &n.FirewallPreset, &n.FirewallPorts,
	}
	err := row.Scan(append(targets, n.nodeEndpointFields.scanTargets()...)...)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch node"})
	}

	annotateXrayVersion(&n)
	if o, ok := h.nodeOccupancy(context.Background())[n.ID]; ok {
		n.OnlineDevices = o.online
		n.Capacity = o.capacity
	}

	labels, err := h.nodeLabels(context.Background(), id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch node labels",
		})
	}
	n.Labels = labels

	return c.JSON(n)
}

// nodeLabels returns the node's per-language names keyed by language code.
func (h *AdminNodeHandler) nodeLabels(ctx context.Context, nodeID string) (map[string]string, error) {
	rows, err := h.db.Query(ctx,
		`SELECT language, name FROM node_labels WHERE node_id = $1`,
		nodeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	labels := make(map[string]string)
	for rows.Next() {
		var code, name string
		if err := rows.Scan(&code, &name); err != nil {
			return nil, err
		}
		labels[code] = name
	}
	return labels, rows.Err()
}

// DeleteNode removes a node by ID.
// @Summary Delete node
// @Description Removes a node by ID
// @Tags admin-nodes
// @Produce json
// @Param id path string true "Node ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id} [delete]
func (h *AdminNodeHandler) DeleteNode(c *fiber.Ctx) error {
	id := c.Params("id")

	name, err := services.DeleteNodeWithManagedDNS(c.UserContext(), h.db, id)
	if err != nil {
		var nodeError *services.RealitySNIError
		if (errors.As(err, &nodeError) && nodeError.Kind == services.RealitySNIMissingNode) || errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "node not found",
			})
		}
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23503" && databaseError.ConstraintName == "node_chains_entry_node_id_fkey" {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "remove the entry node's links before deleting it"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete node",
		})
	}

	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventNodeDeleted,
		Severity:   services.SeverityWarning,
		ActorType:  "admin",
		ActorID:    adminIDOf(c),
		ActorLabel: adminEmail(c),
		TargetType: "node",
		TargetID:   id,
		Detail:     map[string]any{"node": name},
	})

	return c.JSON(fiber.Map{"message": "node deleted"})
}

type tlsStatusResponse struct {
	HasCert  bool   `json:"has_cert"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	Domain   string `json:"domain"`
}

// GetTLSStatus returns the TLS certificate status for a node.
// @Summary Get TLS status
// @Description Returns the TLS certificate status for a node
// @Tags admin-nodes
// @Produce json
// @Param id path string true "Node ID"
// @Success 200 {object} tlsStatusResponse
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id}/tls [get]
func (h *AdminNodeHandler) GetTLSStatus(c *fiber.Ctx) error {
	id := c.Params("id")

	var certFile, keyFile *string
	var domain string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT tls_cert_file, tls_key_file, tls_domain FROM nodes WHERE id = $1`,
		id,
	).Scan(&certFile, &keyFile, &domain)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
	}

	resp := tlsStatusResponse{
		HasCert: certFile != nil && *certFile != "",
		Domain:  domain,
	}
	if certFile != nil {
		resp.CertFile = *certFile
	}
	if keyFile != nil {
		resp.KeyFile = *keyFile
	}

	return c.JSON(resp)
}

type issueCertificateRequest struct {
	Domain string `json:"domain" validate:"required"`
	Email  string `json:"email" validate:"required,email"`
}

// IssueCertificate stores domain info for a node. The node-agent polls
// GET /nodes/{id}/tls-domain (see NodeAgentHandler.GetTLSDomain) and, once it
// sees a non-empty domain, obtains the certificate via ACME itself and
// reports the resulting file paths back through POST /nodes/{id}/tls-cert
// (see NodeAgentHandler.ReportTLSCert) - this handler only records what was
// requested, it does not perform issuance.
// @Summary Issue TLS certificate
// @Description Stores domain info for a node to trigger ACME certificate issuance
// @Tags admin-nodes
// @Accept json
// @Produce json
// @Param id path string true "Node ID"
// @Param body body issueCertificateRequest true "Domain info"
// @Success 202 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id}/tls/issue [post]
func (h *AdminNodeHandler) IssueCertificate(c *fiber.Ctx) error {
	id := c.Params("id")

	var req issueCertificateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.Domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "domain is required",
		})
	}
	if req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "email is required",
		})
	}

	result, err := h.db.Exec(
		context.Background(),
		`UPDATE nodes SET tls_domain = $1, tls_email = $2, updated_at = NOW() WHERE id = $3 AND status != 'pending'`,
		req.Domain, req.Email, id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update node",
		})
	}
	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
	}

	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventNodeTLSRequested,
		Severity:   services.SeverityInfo,
		ActorType:  "admin",
		ActorID:    adminIDOf(c),
		ActorLabel: adminEmail(c),
		TargetType: "node",
		TargetID:   id,
		Detail:     map[string]any{"domain": req.Domain, "email": req.Email},
	})

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"message": "certificate issuance requested",
		"domain":  req.Domain,
	})
}

type xrayVersionResponse struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
}

// GetXrayVersion returns the current Xray version for a node.
// @Summary Get Xray version
// @Description Returns the current Xray version for a node
// @Tags admin-nodes
// @Produce json
// @Param id path string true "Node ID"
// @Success 200 {object} xrayVersionResponse
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id}/xray [get]
func (h *AdminNodeHandler) GetXrayVersion(c *fiber.Ctx) error {
	id := c.Params("id")

	var version string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT COALESCE(xray_version, '') FROM nodes WHERE id = $1 AND status != 'pending'`,
		id,
	).Scan(&version)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
	}

	return c.JSON(xrayVersionResponse{
		CurrentVersion: version,
		LatestVersion:  h.latestXrayVersion(context.Background()),
	})
}

type updateXrayRequest struct {
	Version string `json:"version"`
}

type updateXrayResponse struct {
	Status        string `json:"status"`
	TargetVersion string `json:"target_version"`
}

// UpdateXray requests an Xray version update for a node.
// @Summary Update Xray version
// @Description Requests an Xray version update for a node
// @Tags admin-nodes
// @Accept json
// @Produce json
// @Param id path string true "Node ID"
// @Param body body updateXrayRequest true "Target version"
// @Success 202 {object} updateXrayResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id}/xray/update [post]
func (h *AdminNodeHandler) UpdateXray(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updateXrayRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.Version == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "version is required",
		})
	}

	result, err := h.db.Exec(
		context.Background(),
		`UPDATE nodes SET xray_target_version = $1, updated_at = NOW() WHERE id = $2 AND status != 'pending'`,
		req.Version, id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to request xray update",
		})
	}
	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
	}

	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventNodeXrayUpdateRequested,
		Severity:   services.SeverityInfo,
		ActorType:  "admin",
		ActorID:    adminIDOf(c),
		ActorLabel: adminEmail(c),
		TargetType: "node",
		TargetID:   id,
		Detail:     map[string]any{"target_version": req.Version},
	})

	return c.Status(fiber.StatusAccepted).JSON(updateXrayResponse{
		Status:        "update_requested",
		TargetVersion: req.Version,
	})
}

type updateNodeRequest struct {
	Name               *string             `json:"name"`
	Country            *string             `json:"country"`
	Region             *string             `json:"region"`
	TrafficMultiplier  *float64            `json:"traffic_multiplier"`
	OSFamily           *string             `json:"os_family"`
	Role               *string             `json:"role"`
	PublishDirect      *bool               `json:"publish_direct"`
	MaxConcurrentConns *int                `json:"max_concurrent_conns"`
	FirewallPreset     *string             `json:"firewall_preset"`
	CustomPorts        []nodeprov.PortSpec `json:"custom_ports"`
	// Per-language names keyed by language code. Nil leaves them untouched; a
	// present map replaces the set, and an empty name clears that language.
	Labels map[string]string `json:"labels"`
}

// maxTrafficMultiplier mirrors the CHECK on nodes.traffic_multiplier so a bad
// value returns a message instead of a constraint violation.
const maxTrafficMultiplier = 100.0

// maxNodeLabelLen bounds a per-language node name. Subscription clients show it
// as a server entry, and an overlong one is unreadable there.
const maxNodeLabelLen = 64

// changedColumns recovers column names from "column = $N" SET clauses, so the
// audit event needs no second accumulator that could drift from them.
func changedColumns(setClauses []string) []string {
	out := make([]string, 0, len(setClauses))
	seen := map[string]bool{}
	for _, clause := range setClauses {
		column, _, found := strings.Cut(clause, " = ")
		if !found || seen[column] {
			continue
		}
		seen[column] = true
		out = append(out, column)
	}
	return out
}

// UpdateNode partially updates a node's name, country, or region.
// @Summary Update node
// @Description Partially updates a node's name, country, or region. Cannot edit pending nodes.
// @Tags admin-nodes
// @Accept json
// @Produce json
// @Param id path string true "Node ID"
// @Param body body updateNodeRequest true "Fields to update"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/{id} [put]
func (h *AdminNodeHandler) UpdateNode(c *fiber.Ctx) error {
	id := c.Params("id")
	sniInput, err := parseNodeEndpointInput(c.Body())
	if err != nil {
		var requestError *fiber.Error
		if errors.As(err, &requestError) {
			return c.Status(requestError.Code).JSON(fiber.Map{"error": requestError.Message})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	var req updateNodeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	var requestedRole nodeprov.Role
	if req.Role != nil {
		requestedRole = nodeprov.Role(*req.Role)
		if !requestedRole.Valid() {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("unknown role %q", *req.Role),
			})
		}
	}

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update node",
		})
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if req.Role != nil && !requestedRole.Forwards() {
		rows, err := tx.Query(ctx,
			`SELECT ng.id::text
			 FROM node_groups ng
			 JOIN node_group_nodes ngn ON ngn.node_group_id = ng.id
			 WHERE ngn.node_id = $1
			 ORDER BY ng.id
			 FOR UPDATE OF ng`,
			id,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to lock relay pools",
			})
		}
		for rows.Next() {
			var groupID string
			if err := rows.Scan(&groupID); err != nil {
				rows.Close()
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "failed to lock relay pools",
				})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to lock relay pools",
			})
		}
	}

	// Check node exists and is not pending
	var currentStatus string
	var currentPort int
	var currentPreset string
	var currentRole string
	err = tx.QueryRow(ctx,
		`SELECT status, port, firewall_preset, role FROM nodes WHERE id = $1 FOR UPDATE`,
		id,
	).Scan(&currentStatus, &currentPort, &currentPreset, &currentRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update node"})
	}

	if currentStatus == "pending" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "cannot edit pending node",
		})
	}

	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *req.Name)
		argIdx++
	}
	if req.Country != nil {
		setClauses = append(setClauses, fmt.Sprintf("country = $%d", argIdx))
		args = append(args, *req.Country)
		argIdx++
	}
	if req.Region != nil {
		setClauses = append(setClauses, fmt.Sprintf("region = $%d", argIdx))
		args = append(args, *req.Region)
		argIdx++
	}
	if req.TrafficMultiplier != nil {
		if *req.TrafficMultiplier <= 0 || *req.TrafficMultiplier > maxTrafficMultiplier {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("traffic_multiplier must be greater than 0 and at most %g", maxTrafficMultiplier),
			})
		}
		setClauses = append(setClauses, fmt.Sprintf("traffic_multiplier = $%d", argIdx))
		args = append(args, *req.TrafficMultiplier)
		argIdx++
	}
	if req.OSFamily != nil {
		if f := nodeprov.OSFamily(*req.OSFamily); *req.OSFamily != "" && !f.Valid() {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("unknown os_family %q", *req.OSFamily),
			})
		}
		setClauses = append(setClauses, fmt.Sprintf("os_family = $%d", argIdx))
		args = append(args, *req.OSFamily)
		argIdx++
	}
	if req.MaxConcurrentConns != nil {
		if *req.MaxConcurrentConns < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "max_concurrent_conns cannot be negative",
			})
		}
		setClauses = append(setClauses, fmt.Sprintf("max_concurrent_conns = $%d", argIdx))
		args = append(args, *req.MaxConcurrentConns)
		argIdx++
	}
	if req.PublishDirect != nil {
		setClauses = append(setClauses, fmt.Sprintf("publish_direct = $%d", argIdx))
		args = append(args, *req.PublishDirect)
		argIdx++
	}
	// The effective role decides whether the relay range belongs in the port
	// list, so it is resolved before the firewall block below and fed into it.
	// Promoting a node to relay without re-resolving would leave its entry ports
	// firewalled off, and every chain on it would fail with nothing to point at.
	role := nodeprov.Role(currentRole)
	if req.Role != nil {
		role = requestedRole
		if !role.Forwards() {
			// Reached through the pools this node belongs to, since a chain names
			// a pool rather than a relay. Demoting the last relay in a pool would
			// otherwise leave its chains advertised with nothing forwarding them.
			var attached int
			if err := tx.QueryRow(ctx,
				`SELECT COUNT(*) FROM node_chains c
				 WHERE c.entry_node_id = $1 OR EXISTS (
				   SELECT 1 FROM node_group_nodes ngn
				   WHERE ngn.node_group_id = c.relay_pool_id AND ngn.node_id = $1
				 )`, id,
			).Scan(&attached); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error": "failed to check attached chains",
				})
			}
			if attached > 0 {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{
					"error": fmt.Sprintf("node still relays %d chain(s); remove its links first", attached),
				})
			}
		}
		if !role.Exits() {
			var attached int
			if err := tx.QueryRow(ctx,
				`SELECT COUNT(*) FROM node_chains WHERE exit_node_id = $1
				 AND (entry_node_id IS NOT NULL OR relay_pool_id IS NOT NULL)`, id,
			).Scan(&attached); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to check exit links"})
			}
			if attached > 0 {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "remove exit links before changing its role"})
			}
		}
		setClauses = append(setClauses, fmt.Sprintf("role = $%d", argIdx))
		args = append(args, string(role))
		argIdx++
	}
	// Re-resolve the port list so preset, role and ports cannot disagree.
	if req.FirewallPreset != nil {
		preset := nodeprov.FirewallPreset(*req.FirewallPreset)
		if !preset.Valid() {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("unknown firewall_preset %q", *req.FirewallPreset),
			})
		}
		specs, err := nodeprov.ResolvePorts(role, preset, currentPort, req.CustomPorts)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		setClauses = append(setClauses, fmt.Sprintf("firewall_preset = $%d", argIdx))
		args = append(args, string(preset))
		argIdx++
		setClauses = append(setClauses, fmt.Sprintf("firewall_ports = $%d", argIdx))
		args = append(args, nodeprov.FormatPorts(specs))
		argIdx++
	} else if len(req.CustomPorts) > 0 {
		// Rejected rather than ignored: these would otherwise be silently discarded.
		if nodeprov.FirewallPreset(currentPreset) != nodeprov.PresetCustom {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "custom_ports requires firewall_preset to be custom",
			})
		}
		specs, err := nodeprov.ResolvePorts(role, nodeprov.PresetCustom, currentPort, req.CustomPorts)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		setClauses = append(setClauses, fmt.Sprintf("firewall_ports = $%d", argIdx))
		args = append(args, nodeprov.FormatPorts(specs))
		argIdx++
	} else if req.Role != nil {
		specs, err := nodeprov.ResolvePorts(role, nodeprov.FirewallPreset(currentPreset), currentPort, nil)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		setClauses = append(setClauses, fmt.Sprintf("firewall_ports = $%d", argIdx))
		args = append(args, nodeprov.FormatPorts(specs))
		argIdx++
	}

	if len(setClauses) == 0 && req.Labels == nil && !sniInput.Present {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no fields to update",
		})
	}

	for code, name := range req.Labels {
		if !lang.Code(code).Translatable() {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("unsupported language %q", code),
			})
		}
		if len(name) > maxNodeLabelLen {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("name for %q exceeds %d characters", code, maxNodeLabelLen),
			})
		}
	}

	// A labels-only edit still has to return the node, so fall back to a
	// no-op SET that re-reads the row through the same RETURNING clause.
	if len(setClauses) == 0 {
		setClauses = append(setClauses, "updated_at = NOW()")
	}

	query := fmt.Sprintf(
		"UPDATE nodes SET %s WHERE id = $%d RETURNING id, name, country, region, ip::text, port, status, xray_version, traffic_multiplier, os_family, max_concurrent_conns, firewall_preset, firewall_ports, created_at",
		strings.Join(setClauses, ", "), argIdx,
	)
	args = append(args, id)

	var n struct {
		nodeEndpointFields
		ID          string `json:"id"`
		Name        string `json:"name"`
		Country     string `json:"country"`
		Region      string `json:"region"`
		IP          string `json:"ip"`
		Port        int    `json:"port"`
		Status      string `json:"status"`
		XrayVersion string `json:"xray_version"`

		TrafficMultiplier  float64   `json:"traffic_multiplier"`
		OSFamily           string    `json:"os_family"`
		MaxConcurrentConns int       `json:"max_concurrent_conns"`
		FirewallPreset     string    `json:"firewall_preset"`
		FirewallPorts      string    `json:"firewall_ports"`
		CreatedAt          time.Time `json:"created_at"`

		Labels map[string]string `json:"labels,omitempty"`
	}

	err = tx.QueryRow(ctx, query, args...).Scan(
		&n.ID, &n.Name, &n.Country, &n.Region, &n.IP, &n.Port,
		&n.Status, &n.XrayVersion, &n.TrafficMultiplier,
		&n.OSFamily, &n.MaxConcurrentConns, &n.FirewallPreset, &n.FirewallPorts,
		&n.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "node not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update node"})
	}
	if sniInput.Present {
		locked, err := services.LockRealityNode(ctx, tx, id)
		if err != nil {
			return nodeSNIErrorResponse(c, err)
		}
		if err := services.UpdateCanonicalRealitySNI(ctx, tx, locked, sniInput.Value); err != nil {
			return nodeSNIErrorResponse(c, err)
		}
	}

	for code, name := range req.Labels {
		if name == "" {
			_, err = tx.Exec(ctx,
				`DELETE FROM node_labels WHERE node_id = $1 AND language = $2`,
				id, code,
			)
		} else {
			_, err = tx.Exec(ctx,
				`INSERT INTO node_labels (node_id, language, name) VALUES ($1, $2, $3)
				 ON CONFLICT (node_id, language) DO UPDATE SET name = EXCLUDED.name`,
				id, code, name,
			)
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update node labels",
			})
		}
	}
	if err := tx.QueryRow(ctx, `SELECT nodes.id`+nodeEndpointSelection+` WHERE nodes.id = $1`, id).
		Scan(append([]any{&n.ID}, n.nodeEndpointFields.scanTargets()...)...); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update node"})
	}

	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update node",
		})
	}

	if req.Labels != nil {
		labels, err := h.nodeLabels(ctx, id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to fetch node labels",
			})
		}
		n.Labels = labels
	}

	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventNodeUpdated,
		Severity:   services.SeverityInfo,
		ActorType:  "admin",
		ActorID:    adminIDOf(c),
		ActorLabel: adminEmail(c),
		TargetType: "node",
		TargetID:   id,
		Detail:     map[string]any{"node": n.Name, "changed": changedColumns(setClauses)},
	})

	return c.JSON(n)
}
