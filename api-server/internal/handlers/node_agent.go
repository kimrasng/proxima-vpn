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

	_, err = h.db.Exec(
		context.Background(),
		`UPDATE nodes
		 SET name = $1, ip = $2::inet, port = $3, xray_version = $4,
		     country = $5, region = $6, api_key = $7, reg_token = NULL, status = 'offline'
		 WHERE id = $8`,
		req.Name, req.IP, req.Port, req.XrayVersion,
		req.Country, req.Region, apiKey, nodeID,
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
		     network_in = $5, network_out = $6, last_seen = NOW(), status = 'online'
		 WHERE id = $7`,
		req.CPUUsage, req.MemoryUsage, req.DiskUsage, req.LoadAvg,
		req.NetworkIn, req.NetworkOut, nodeID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update heartbeat",
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
