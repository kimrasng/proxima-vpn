package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// AdminNodeHandler handles admin node management endpoints.
type AdminNodeHandler struct {
	db       *pgxpool.Pool
	redis    *redis.Client
	panelURL string
}

// NewAdminNodeHandler creates a new AdminNodeHandler.
func NewAdminNodeHandler(db *pgxpool.Pool, rdb *redis.Client, panelURL string) *AdminNodeHandler {
	return &AdminNodeHandler{
		db:       db,
		redis:    rdb,
		panelURL: panelURL,
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
	defer resp.Body.Close()
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
}

// GenerateToken creates a one-time registration token for a new node.
// @Summary Generate node registration token
// @Description Creates a one-time registration token and install command for a new node
// @Tags admin-nodes
// @Produce json
// @Success 200 {object} generateTokenResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/nodes/token [post]
func (h *AdminNodeHandler) GenerateToken(c *fiber.Ctx) error {
	token := crypto.GenerateRandomString(32)

	_, err := h.db.Exec(
		context.Background(),
		`INSERT INTO nodes (name, reg_token, api_key, country, region, ip, port, status)
		 VALUES ('pending', $1, 'pending', '', '', '0.0.0.0'::inet, 443, 'pending')`,
		token,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to generate registration token",
		})
	}

	installCmd := fmt.Sprintf(
		"bash <(curl -s %s/scripts/install.sh) --server %s --token %s",
		h.panelURL, h.panelURL, token,
	)

	return c.JSON(generateTokenResponse{
		Token:          token,
		InstallCommand: installCmd,
	})
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
		`SELECT id, name, country, region, ip::text, port, status, xray_version,
		        cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out,
		        last_seen, created_at, updated_at, last_ping_at
		 FROM nodes ORDER BY created_at DESC`,
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
		if err := rows.Scan(
			&n.ID, &n.Name, &n.Country, &n.Region, &n.IP, &n.Port,
			&n.Status, &n.XrayVersion,
			&n.CPUUsage, &n.MemoryUsage, &n.DiskUsage, &n.LoadAvg, &n.NetworkIn, &n.NetworkOut,
			&n.LastSeen, &n.CreatedAt, &n.UpdatedAt, &n.LastPingAt,
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan node",
			})
		}
		nodes = append(nodes, n)
	}

	return c.JSON(nodes)
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
	err := h.db.QueryRow(
		context.Background(),
		`SELECT id, name, country, region, ip::text, port, status, xray_version,
		        cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out,
		        last_seen, created_at, updated_at, last_ping_at
		 FROM nodes WHERE id = $1`,
		id,
	).Scan(
		&n.ID, &n.Name, &n.Country, &n.Region, &n.IP, &n.Port,
		&n.Status, &n.XrayVersion,
		&n.CPUUsage, &n.MemoryUsage, &n.DiskUsage, &n.LoadAvg, &n.NetworkIn, &n.NetworkOut,
		&n.LastSeen, &n.CreatedAt, &n.UpdatedAt, &n.LastPingAt,
	)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
	}

	return c.JSON(n)
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

	result, err := h.db.Exec(
		context.Background(),
		`DELETE FROM nodes WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete node",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
	}

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

// IssueCertificate stores domain info for a node so the node-agent can issue a TLS certificate via ACME.
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

	result, err := h.db.Exec(
		context.Background(),
		`UPDATE nodes SET tls_domain = $1, updated_at = NOW() WHERE id = $2 AND status != 'pending'`,
		req.Domain, id,
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

	return c.Status(fiber.StatusAccepted).JSON(updateXrayResponse{
		Status:        "update_requested",
		TargetVersion: req.Version,
	})
}

type updateNodeRequest struct {
	Name    *string `json:"name"`
	Country *string `json:"country"`
	Region  *string `json:"region"`
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

	var req updateNodeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Check node exists and is not pending
	var currentStatus string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT status FROM nodes WHERE id = $1`,
		id,
	).Scan(&currentStatus)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
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

	if len(setClauses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no fields to update",
		})
	}

	query := fmt.Sprintf(
		"UPDATE nodes SET %s WHERE id = $%d RETURNING id, name, country, region, ip::text, port, status, xray_version, created_at",
		strings.Join(setClauses, ", "), argIdx,
	)
	args = append(args, id)

	var n struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Country     string    `json:"country"`
		Region      string    `json:"region"`
		IP          string    `json:"ip"`
		Port        int       `json:"port"`
		Status      string    `json:"status"`
		XrayVersion string    `json:"xray_version"`
		CreatedAt   time.Time `json:"created_at"`
	}

	err = h.db.QueryRow(context.Background(), query, args...).Scan(
		&n.ID, &n.Name, &n.Country, &n.Region, &n.IP, &n.Port,
		&n.Status, &n.XrayVersion, &n.CreatedAt,
	)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "node not found",
		})
	}

	return c.JSON(n)
}
