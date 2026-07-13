package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
	"github.com/proximavpn/proxima-vpn/pkg/speedtier"
)

type SubscriptionHandler struct {
	db             *pgxpool.Pool
	updateInterval int
}

func NewSubscriptionHandler(db *pgxpool.Pool, updateInterval int) *SubscriptionHandler {
	if updateInterval <= 0 {
		updateInterval = 3600
	}
	return &SubscriptionHandler{db: db, updateInterval: updateInterval}
}

type subscriptionUser struct {
	ID            string
	PlanID        string
	IsActive      bool
	Status        string
	TrafficUsed   int64
	TrafficLimit  *int64
	SpeedLimit    *int64
	PlanExpiresAt *time.Time
}

type subscriptionNode struct {
	ID               string
	Name             string
	IP               string
	Port             int
	Status           string
	RealityPublicKey string
	RealityShortID   string
	TLSCertFile      *string
	TLSKeyFile       *string
	SSPassword       *string
}

// GetSubscription returns the subscription configuration for a device.
// @Summary Get subscription
// @Description Returns proxy configuration for a device based on subscription token
// @Tags subscription
// @Produce plain
// @Param sub_token path string true "Subscription token"
// @Param device_id path string true "Device ID"
// @Success 200 {string} string "Base64-encoded proxy configuration"
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /sub/{sub_token}/{device_id} [get]
func (h *SubscriptionHandler) GetSubscription(c *fiber.Ctx) error {
	subToken := c.Params("sub_token")
	deviceID := c.Params("device_id")

	if subToken == "" || deviceID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "sub_token and device_id are required",
		})
	}

	ctx := context.Background()

	var user subscriptionUser
	var deviceUUID string
	err := h.db.QueryRow(ctx,
		`SELECT u.id, u.plan_id, u.is_active, u.status, u.traffic_used,
		        p.traffic_limit, p.speed_limit, u.plan_expires_at, d.xray_uuid
		 FROM users u
		 JOIN devices d ON d.user_id = u.id
		 LEFT JOIN plans p ON u.plan_id = p.id
		 WHERE u.sub_token = $1 AND d.id = $2`,
		subToken, deviceID,
	).Scan(
		&user.ID, &user.PlanID, &user.IsActive, &user.Status,
		&user.TrafficUsed, &user.TrafficLimit, &user.SpeedLimit, &user.PlanExpiresAt, &deviceUUID,
	)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "subscription not found",
		})
	}

	if !user.IsActive || user.Status != "active" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "subscription inactive",
		})
	}

	rows, err := h.db.Query(ctx,
		`SELECT n.id, n.name, host(n.ip), n.port, n.status, n.reality_public_key, n.reality_short_id,
		        n.tls_cert_file, n.tls_key_file, n.ss_password
		 FROM nodes n
		 JOIN node_group_nodes ngn ON n.id = ngn.node_id
		 JOIN node_groups ng ON ngn.node_group_id = ng.id
		 JOIN plans p ON p.node_group_id = ng.id
		 WHERE p.id = $1 AND n.status != 'pending'`,
		user.PlanID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch nodes",
		})
	}
	defer rows.Close()

	var nodes []subscriptionNode
	for rows.Next() {
		var node subscriptionNode
		if err := rows.Scan(
			&node.ID, &node.Name, &node.IP, &node.Port,
			&node.Status, &node.RealityPublicKey, &node.RealityShortID,
			&node.TLSCertFile, &node.TLSKeyFile, &node.SSPassword,
		); err != nil {
			continue
		}
		nodes = append(nodes, node)
	}

	// Plan speed limit determines which port a client connects to (a dedicated
	// tc-shaped port) and, for limited plans, restricts them to VLESS only so
	// the limit cannot be bypassed via other protocols.
	speedMbps := 0
	if user.SpeedLimit != nil && *user.SpeedLimit > 0 {
		speedMbps = int(*user.SpeedLimit)
	}

	format := strings.ToLower(c.Query("format"))
	if format == "" {
		format = detectFormatFromUA(c.Get("User-Agent"))
	}

	var body []byte
	var contentType string

	switch format {
	case "clash":
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps)
		infoLabels := buildPlanInfoLabels(user)
		result, err := subscription.GenerateClash(nodeInfos, deviceUUID, infoLabels)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate clash config"})
		}
		body = result
		contentType = "text/yaml; charset=utf-8"

	case "singbox":
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps)
		result, err := subscription.GenerateSingbox(nodeInfos, deviceUUID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate singbox config"})
		}
		body = result
		contentType = "application/json; charset=utf-8"

	case "surfboard":
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps)
		result, err := subscription.GenerateSurfboard(nodeInfos, deviceUUID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate surfboard config"})
		}
		body = result
		contentType = "text/plain; charset=utf-8"

	case "quantumult":
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps)
		result, err := subscription.GenerateQuantumult(nodeInfos, deviceUUID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate quantumult config"})
		}
		body = result
		contentType = "text/plain; charset=utf-8"

	default:
		var links []string
		for _, node := range nodes {
			fragment := node.Name
			if node.Status == "offline" {
				fragment += " [OFFLINE]"
			}

			links = append(links, buildVLESSLink(deviceUUID, node, speedtier.VlessPort(node.Port, speedMbps), fragment))

			// Speed-limited plans are VLESS-only (see buildNodeInfoList).
			if speedMbps > 0 {
				continue
			}

			hasTLS := node.TLSCertFile != nil && node.TLSKeyFile != nil &&
				*node.TLSCertFile != "" && *node.TLSKeyFile != ""

			if hasTLS {
				links = append(links, buildVMESSLink(deviceUUID, node, fragment))
				links = append(links, buildTrojanLink(deviceUUID, node, fragment))
			}

			if node.SSPassword != nil && *node.SSPassword != "" {
				links = append(links, buildSSLink(*node.SSPassword, node, fragment))
			}
		}
		body = []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n"))))
		contentType = "text/plain; charset=utf-8"
	}

	var totalTraffic int64
	if user.TrafficLimit != nil {
		totalTraffic = *user.TrafficLimit
	}

	var expireTimestamp int64
	if user.PlanExpiresAt != nil {
		expireTimestamp = user.PlanExpiresAt.Unix()
	}

	userinfo := fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d",
		user.TrafficUsed, totalTraffic, expireTimestamp)

	updateInterval := h.updateInterval
	var dbInterval int
	err = h.db.QueryRow(ctx,
		`SELECT value::int FROM settings WHERE key = 'subscription_update_interval'`,
	).Scan(&dbInterval)
	if err == nil && dbInterval > 0 {
		updateInterval = dbInterval
	}

	c.Set("Content-Type", contentType)
	c.Set("Profile-Update-Interval", fmt.Sprintf("%d", updateInterval))
	c.Set("Subscription-Userinfo", userinfo)

	return c.Send(body)
}

func detectFormatFromUA(ua string) string {
	uaLower := strings.ToLower(ua)
	switch {
	case strings.Contains(uaLower, "clash"):
		return "clash"
	case strings.Contains(uaLower, "singbox"), strings.Contains(uaLower, "sing-box"):
		return "singbox"
	case strings.Contains(uaLower, "surfboard"):
		return "surfboard"
	case strings.Contains(uaLower, "quantumult"):
		return "quantumult"
	default:
		return "v2ray"
	}
}

// buildPlanInfoLabels builds Korean informational pseudo-node labels shown in
// Clash (remaining traffic and days until plan expiry).
func buildPlanInfoLabels(user subscriptionUser) []string {
	var labels []string

	if user.TrafficLimit != nil && *user.TrafficLimit > 0 {
		remaining := *user.TrafficLimit - user.TrafficUsed
		if remaining < 0 {
			remaining = 0
		}
		gb := float64(remaining) / (1024 * 1024 * 1024)
		labels = append(labels, fmt.Sprintf("잔여 트래픽: %.1f GB", gb))
	} else {
		labels = append(labels, "잔여 트래픽: 무제한")
	}

	if user.PlanExpiresAt != nil {
		days := int(math.Ceil(time.Until(*user.PlanExpiresAt).Hours() / 24))
		if days < 0 {
			days = 0
		}
		labels = append(labels, fmt.Sprintf("만료까지: %d일", days))
	}

	return labels
}

func buildNodeInfoList(nodes []subscriptionNode, userUUID string, speedMbps int) []subscription.NodeInfo {
	var infos []subscription.NodeInfo
	for _, node := range nodes {
		infos = append(infos, subscription.NodeInfo{
			Name:             node.Name,
			IP:               node.IP,
			Port:             speedtier.VlessPort(node.Port, speedMbps),
			Protocol:         "vless_reality",
			RealityPublicKey: node.RealityPublicKey,
			RealityShortID:   node.RealityShortID,
		})

		// Speed-limited plans are VLESS-only so the tc limit cannot be bypassed.
		if speedMbps > 0 {
			continue
		}

		hasTLS := node.TLSCertFile != nil && node.TLSKeyFile != nil &&
			*node.TLSCertFile != "" && *node.TLSKeyFile != ""

		if hasTLS {
			infos = append(infos, subscription.NodeInfo{
				Name:       node.Name + " VMess",
				IP:         node.IP,
				Port:       8443,
				Protocol:   "vmess_ws",
				WSPath:     "/vmess",
				TLSEnabled: true,
				ServerName: node.IP,
			})

			infos = append(infos, subscription.NodeInfo{
				Name:       node.Name + " Trojan",
				IP:         node.IP,
				Port:       2083,
				Protocol:   "trojan_tls",
				TLSEnabled: true,
				ServerName: node.IP,
			})
		}

		if node.SSPassword != nil && *node.SSPassword != "" {
			infos = append(infos, subscription.NodeInfo{
				Name:       node.Name + " SS",
				IP:         node.IP,
				Port:       8388,
				Protocol:   "shadowsocks",
				SSMethod:   "2022-blake3-aes-128-gcm",
				SSPassword: *node.SSPassword,
			})
		}
	}
	return infos
}

func buildVLESSLink(uuid string, node subscriptionNode, port int, fragment string) string {
	return fmt.Sprintf(
		"vless://%s@%s:%d?type=tcp&security=reality&sni=www.cloudflare.com&fp=chrome&pbk=%s&sid=%s&flow=xtls-rprx-vision#%s",
		uuid,
		node.IP,
		port,
		url.QueryEscape(node.RealityPublicKey),
		url.QueryEscape(node.RealityShortID),
		url.PathEscape(fragment+" VLESS"),
	)
}

func buildVMESSLink(uuid string, node subscriptionNode, fragment string) string {
	vmessConfig := map[string]interface{}{
		"v":    "2",
		"ps":   fragment + " VMess",
		"add":  node.IP,
		"port": 8443,
		"id":   uuid,
		"aid":  0,
		"net":  "ws",
		"type": "none",
		"host": node.IP,
		"path": "/vmess",
		"tls":  "tls",
	}
	jsonBytes, _ := json.Marshal(vmessConfig)
	return "vmess://" + base64.StdEncoding.EncodeToString(jsonBytes)
}

func buildTrojanLink(uuid string, node subscriptionNode, fragment string) string {
	return fmt.Sprintf(
		"trojan://%s@%s:%d?security=tls&type=tcp#%s",
		uuid,
		node.IP,
		2083,
		url.PathEscape(fragment+" Trojan"),
	)
}

func buildSSLink(password string, node subscriptionNode, fragment string) string {
	method := "2022-blake3-aes-128-gcm"
	userinfo := base64.URLEncoding.EncodeToString([]byte(method + ":" + password))
	return fmt.Sprintf(
		"ss://%s@%s:%d#%s",
		userinfo,
		node.IP,
		8388,
		url.PathEscape(fragment+" SS"),
	)
}
