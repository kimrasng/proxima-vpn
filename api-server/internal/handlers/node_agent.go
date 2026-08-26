package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/proximavpn/proxima-vpn/api-server/internal/metrics"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

type NodeAgentHandler struct {
	db           *pgxpool.Pool
	redis        *redis.Client
	xrayConfigSvc *services.XrayConfigService
}

func NewNodeAgentHandler(db *pgxpool.Pool, rdb *redis.Client) *NodeAgentHandler {
	return &NodeAgentHandler{
		db:           db,
		redis:        rdb,
		xrayConfigSvc: services.NewXrayConfigService(db),
	}
}

type registerNodeRequest struct {
	RegToken    string `json:"reg_token"`
	IP          string `json:"ip"`
	Port        int    `json:"port"`
	XrayVersion string `json:"xray_version"`
	Name        string `json:"name"`
	Country     string `json:"country"`
	Region      string `json:"region"`
}

type registerNodeResponse struct {
	NodeID string `json:"node_id"`
	APIKey string `json:"api_key"`
}

func (h *NodeAgentHandler) Register(c *fiber.Ctx) error {
	var req registerNodeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.RegToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "reg_token is required",
		})
	}

	var nodeID string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT id FROM nodes WHERE reg_token = $1 AND status = 'pending'`,
		req.RegToken,
	).Scan(&nodeID)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid registration token",
		})
	}

	apiKey := crypto.GenerateAPIKey()

	realityPrivateKey, realityPublicKey, err := crypto.GenerateRealityKeypair()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to generate reality keys",
		})
	}
	realityShortID := crypto.GenerateRealityShortID()

	_, err = h.db.Exec(
		context.Background(),
		`UPDATE nodes
		 SET name = $1, ip = $2::inet, port = $3, xray_version = $4,
		     country = $5, region = $6, api_key = $7, reg_token = NULL, status = 'offline',
		     reality_private_key = COALESCE(NULLIF(reality_private_key, ''), $8),
		     reality_public_key  = COALESCE(NULLIF(reality_public_key, ''), $9),
		     reality_short_id    = COALESCE(NULLIF(reality_short_id, ''), $10)
		 WHERE id = $11`,
		req.Name, req.IP, req.Port, req.XrayVersion,
		req.Country, req.Region, apiKey,
		realityPrivateKey, realityPublicKey, realityShortID, nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to register node",
		})
	}

	return c.JSON(registerNodeResponse{
		NodeID: nodeID,
		APIKey: apiKey,
	})
}

func (h *NodeAgentHandler) Config(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	configJSON, err := h.xrayConfigSvc.GenerateConfig(context.Background(), nodeID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to generate config",
		})
	}

	c.Set("Content-Type", "application/json")
	return c.Send(configJSON)
}

type heartbeatRequest struct {
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage float64 `json:"memory_usage"`
	DiskUsage   float64 `json:"disk_usage"`
	LoadAvg     float64 `json:"load_avg"`
	NetworkIn   float64 `json:"network_in"`
	NetworkOut  float64 `json:"network_out"`
	XrayVersion string  `json:"xray_version"`
}

func (h *NodeAgentHandler) Heartbeat(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	var req heartbeatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	_, err := h.db.Exec(
		context.Background(),
		`UPDATE nodes
		 SET cpu_usage = $1, memory_usage = $2, disk_usage = $3, load_avg = $4,
		     network_in = $5, network_out = $6, last_seen = NOW(), status = 'online',
		     xray_version = COALESCE(NULLIF($7, ''), xray_version)
		 WHERE id = $8`,
		req.CPUUsage, req.MemoryUsage, req.DiskUsage, req.LoadAvg,
		req.NetworkIn, req.NetworkOut, req.XrayVersion, nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update heartbeat",
		})
	}

	_, err = h.db.Exec(
		context.Background(),
		`INSERT INTO node_metrics_history
		 (node_id, cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		nodeID, req.CPUUsage, req.MemoryUsage, req.DiskUsage, req.LoadAvg,
		req.NetworkIn, req.NetworkOut,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to record metrics history",
		})
	}

	metrics.NodeCPUUsage.WithLabelValues(nodeID).Set(req.CPUUsage)
	metrics.NodeMemoryUsage.WithLabelValues(nodeID).Set(req.MemoryUsage)

	return c.JSON(fiber.Map{"status": "ok"})
}

type inboundConfigEntry struct {
	ID       string          `json:"id"`
	Protocol string          `json:"protocol"`
	Port     int             `json:"port"`
	Tag      string          `json:"tag"`
	Settings json.RawMessage `json:"settings"`
	Enabled  bool            `json:"enabled"`
}

func (h *NodeAgentHandler) GetInbounds(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, protocol, port, tag, settings, enabled
		 FROM inbounds WHERE node_id = $1 ORDER BY created_at`,
		nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch inbounds",
		})
	}
	defer rows.Close()

	inbounds := make([]inboundConfigEntry, 0)
	for rows.Next() {
		var ib inboundConfigEntry
		if err := rows.Scan(&ib.ID, &ib.Protocol, &ib.Port, &ib.Tag, &ib.Settings, &ib.Enabled); err != nil {
			continue
		}
		inbounds = append(inbounds, ib)
	}

	return c.JSON(inbounds)
}

type wireGuardPeerEntry struct {
	PublicKey  string `json:"public_key"`
	AllowedIPs string `json:"allowed_ips"`
}

// GetWireGuardPeers returns the WireGuard peers (device public key + tunnel
// address) that should currently be admitted on this node's wg0 interface.
// The eligibility predicate mirrors XrayConfigService.GenerateConfig's active-
// client join (services/xray_config.go) so WireGuard access is revoked on the
// same conditions - suspension, expiry, traffic limit - as every other
// protocol. Speed-limited plans are excluded entirely (not just routed
// elsewhere, as xray_config.go does with a dedicated tc-shaped VLESS port)
// because WireGuard has no equivalent shaping hook; letting them through
// would bypass their speed cap. The node-agent polls this and diffs against
// what it last applied (see inboundsPollLoop/syncWireGuardPeers in
// node-agent/cmd/main.go).
func (h *NodeAgentHandler) GetWireGuardPeers(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	rows, err := h.db.Query(
		context.Background(),
		`SELECT d.wg_public_key, d.wg_address
		 FROM devices d
		 JOIN users u ON d.user_id = u.id
		 JOIN plans p ON u.plan_id = p.id
		 JOIN node_groups ng ON p.node_group_id = ng.id
		 JOIN node_group_nodes ngn ON ng.id = ngn.node_group_id
		 WHERE ngn.node_id = $1
		   AND u.is_active = true
		   AND u.status = 'active'
		   AND (u.plan_expires_at IS NULL OR u.plan_expires_at > NOW())
		   AND (p.traffic_limit IS NULL OR u.traffic_used < p.traffic_limit)
		   AND (p.speed_limit IS NULL OR p.speed_limit <= 0)
		   AND d.wg_public_key IS NOT NULL AND d.wg_public_key != ''`,
		nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch wireguard peers",
		})
	}
	defer rows.Close()

	peers := make([]wireGuardPeerEntry, 0)
	for rows.Next() {
		var p wireGuardPeerEntry
		if err := rows.Scan(&p.PublicKey, &p.AllowedIPs); err != nil {
			continue
		}
		peers = append(peers, p)
	}

	return c.JSON(peers)
}

// GetHysteria2Users returns the device xray_uuids currently eligible to
// authenticate against this node's Hysteria2 server. Each uuid is used as
// both username and password (see buildHysteria2Config in node-agent/cmd/
// main.go), matching subscription/singbox.go:singboxHysteria2's existing
// `password: userUUID` expectation. Eligibility mirrors GetWireGuardPeers and
// XrayConfigService.GenerateConfig so Hysteria2 access is revoked on the same
// conditions as every other protocol.
func (h *NodeAgentHandler) GetHysteria2Users(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	rows, err := h.db.Query(
		context.Background(),
		`SELECT d.xray_uuid
		 FROM devices d
		 JOIN users u ON d.user_id = u.id
		 JOIN plans p ON u.plan_id = p.id
		 JOIN node_groups ng ON p.node_group_id = ng.id
		 JOIN node_group_nodes ngn ON ng.id = ngn.node_group_id
		 WHERE ngn.node_id = $1
		   AND u.is_active = true
		   AND u.status = 'active'
		   AND (u.plan_expires_at IS NULL OR u.plan_expires_at > NOW())
		   AND (p.traffic_limit IS NULL OR u.traffic_used < p.traffic_limit)
		   AND (p.speed_limit IS NULL OR p.speed_limit <= 0)`,
		nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch hysteria2 users",
		})
	}
	defer rows.Close()

	uuids := make([]string, 0)
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			continue
		}
		uuids = append(uuids, uuid)
	}

	return c.JSON(uuids)
}

type tlsDomainResponse struct {
	Domain string `json:"domain"`
	Email  string `json:"email"`
}

// GetTLSDomain returns the TLS domain/email an admin requested for this node
// via AdminNodeHandler.IssueCertificate (admin_node.go), if any. The
// node-agent polls this and, when it sees a non-empty domain, obtains the
// certificate itself via ACME (node-agent/internal/cert) and reports the
// resulting file paths back through ReportTLSCert below.
func (h *NodeAgentHandler) GetTLSDomain(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	var domain, email string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT tls_domain, tls_email FROM nodes WHERE id = $1`,
		nodeID,
	).Scan(&domain, &email)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch node",
		})
	}

	return c.JSON(tlsDomainResponse{Domain: domain, Email: email})
}

type reportTLSCertRequest struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// ReportTLSCert records the local file paths of a certificate the node-agent
// just obtained via ACME, so XrayConfigService.GenerateConfig can wire them
// into vmess_ws/trojan_tls inbounds (see buildVmessWS/buildTrojanTLS in
// services/xray_config.go). Paths are node-local: Xray and the node-agent
// that obtained the cert run on the same host.
func (h *NodeAgentHandler) ReportTLSCert(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	var req reportTLSCertRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.CertFile == "" || req.KeyFile == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "cert_file and key_file are required",
		})
	}

	_, err := h.db.Exec(
		context.Background(),
		`UPDATE nodes SET tls_cert_file = $1, tls_key_file = $2, updated_at = NOW() WHERE id = $3`,
		req.CertFile, req.KeyFile, nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update node",
		})
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

type statEntry struct {
	XrayUUID string `json:"xray_uuid"`
	UpBytes  int64  `json:"up_bytes"`
	DnBytes  int64  `json:"dn_bytes"`
}

type statsRequest struct {
	Stats       []statEntry `json:"stats"`
	OnlineUUIDs []string    `json:"online_uuids"`
}

func (h *NodeAgentHandler) Stats(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)

	var req statsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	ctx := context.Background()

	for _, s := range req.Stats {
		var deviceID string
		err := h.db.QueryRow(ctx,
			`SELECT id FROM devices WHERE xray_uuid = $1`,
			s.XrayUUID,
		).Scan(&deviceID)
		if err != nil {
			continue
		}

		_, _ = h.db.Exec(ctx,
			`INSERT INTO traffic_logs (device_id, node_id, up_bytes, dn_bytes) VALUES ($1, $2, $3, $4)`,
			deviceID, nodeID, s.UpBytes, s.DnBytes,
		)

		_, _ = h.db.Exec(ctx,
			`UPDATE users SET traffic_used = traffic_used + $1
			 WHERE id = (SELECT user_id FROM devices WHERE xray_uuid = $2)`,
			s.UpBytes+s.DnBytes, s.XrayUUID,
		)

		metrics.TrafficBytesTotal.WithLabelValues("up").Add(float64(s.UpBytes))
		metrics.TrafficBytesTotal.WithLabelValues("down").Add(float64(s.DnBytes))
	}

	if len(req.OnlineUUIDs) > 0 {
		data, _ := json.Marshal(req.OnlineUUIDs)
		key := fmt.Sprintf("node:%s:online", nodeID)
		h.redis.Set(ctx, key, string(data), 60*time.Second)
	}

	return c.JSON(fiber.Map{"status": "ok"})
}
