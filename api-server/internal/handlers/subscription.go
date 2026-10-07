package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services/subscription"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
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
	Language      string
}

type subscriptionNode struct {
	ID                    string
	Name                  string
	IP                    string
	Port                  int
	Status                string
	RealityPublicKey      string
	RealityShortID        string
	TLSCertFile           *string
	TLSKeyFile            *string
	VmessPort             *int
	TrojanPort            *int
	SSPort                *int
	SSPassword            *string
	WGPort                *int
	WGServerPrivateKey    *string
	HY2Port               *int
	RealityPorts          []int
	GeneratedRealityPorts []int
	RealitySNI            string
	RealityStatus         string
	ConfigHash            string
	XrayRunning           bool
	HeartbeatFresh        bool
	RealityReady          bool
	DeviceBandwidthReady  bool

	// Chain fields. EntryHost/EntryPort are empty on a direct chain, which is
	// reached at the exit's own address; on a relayed chain they are the address
	// the client dials, and ChainExitPort says which of the exit's ports the relay
	// forwards it to - i.e. which protocol this chain carries.
	EntryHost     string
	EntryPort     *int
	ChainExitPort int
}

// relayed reports whether this row is reached through a relay pool.
func (n subscriptionNode) relayed() bool { return n.EntryPort != nil }

// dialHost is the address a client connects to: the chain's entry host when it is
// relayed, and the exit's own address otherwise. EntryHost is preferred over the
// relay's IP so the advertised name stays fixed while the addresses behind it are
// replaced.
func (n subscriptionNode) dialHost() string {
	if n.relayed() {
		return strings.TrimSpace(n.EntryHost)
	}
	return n.IP
}

// GetSubscription serves /sub/{sub_token}/{second}. The second segment is
// either a client type override (clash-meta, sing-box, ...) for the account URL
// or, for links issued before the account URL, a device ID.
// @Summary Get subscription
// @Description Returns proxy configuration for a client type or a legacy device
// @Tags subscription
// @Produce plain
// @Param sub_token path string true "Subscription token"
// @Param device_id path string true "Client type (clash-meta, clash, sing-box, v2ray, wireguard) or legacy device ID"
// @Success 200 {string} string "Proxy configuration"
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /sub/{sub_token}/{device_id} [get]
func (h *SubscriptionHandler) GetSubscription(c *fiber.Ctx) error {
	second := c.Params("device_id")
	if format, ok := pathClientTypes[strings.ToLower(second)]; ok {
		return h.getAccountSubscription(c, format)
	}
	return h.getSubscriptionForDevice(c, second, "")
}

func (h *SubscriptionHandler) getSubscriptionForDevice(c *fiber.Ctx, deviceID, formatOverride string) error {
	subToken := c.Params("sub_token")
	if subToken == "" || deviceID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "sub_token and device_id are required",
		})
	}

	ctx := context.Background()

	var user subscriptionUser
	var deviceUUID string
	var deviceWGPrivateKey, deviceWGAddress *string
	err := h.db.QueryRow(ctx,
		`SELECT u.id, u.plan_id, u.is_active, u.status, u.traffic_used,
		        p.traffic_limit, p.speed_limit, u.plan_expires_at, d.xray_uuid,
		        d.wg_private_key, d.wg_address, u.language
		 FROM users u
		 JOIN devices d ON d.user_id = u.id
		 LEFT JOIN plans p ON u.plan_id = p.id
		 WHERE u.sub_token = $1 AND d.id = $2 AND d.retired_at IS NULL`,
		subToken, deviceID,
	).Scan(
		&user.ID, &user.PlanID, &user.IsActive, &user.Status,
		&user.TrafficUsed, &user.TrafficLimit, &user.SpeedLimit, &user.PlanExpiresAt, &deviceUUID,
		&deviceWGPrivateKey, &deviceWGAddress, &user.Language,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "subscription not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch subscription",
		})
	}

	if status, message := subscriptionEligibility(user); status != 0 {
		return c.Status(status).JSON(fiber.Map{"error": message})
	}

	rows, err := h.db.Query(ctx,
		`SELECT n.id, COALESCE(NULLIF(nl.name, ''), NULLIF(c.name, ''), n.name), host(n.ip), n.port, n.status,
		        n.reality_public_key, n.reality_short_id,
		        n.tls_cert_file, n.tls_key_file,
		        vmessib.port, trojanib.port, ssib.port, ssib.settings->>'password',
		        wgib.port, wgib.settings->>'private_key',
		        hy2ib.port,
		        COALESCE(CASE WHEN c.entry_node_id IS NOT NULL THEN med.hostname ELSE c.entry_host END, ''),
		        c.entry_port, c.exit_port,
		        realityib.ports, COALESCE(n.reality_client_sni, ''), COALESCE(n.reality_sni_status, ''),
		        COALESCE(n.config_hash, ''), COALESCE(n.xray_running, false),
		        COALESCE(n.last_seen >= NOW() - INTERVAL '40 seconds', false),
              COALESCE(n.shaping_ok,false) AND n.shaping_mode = 'device_global_v1'
		 FROM node_chains c
		 JOIN node_group_chains ngc ON ngc.chain_id = c.id
		 JOIN node_groups ng ON ngc.node_group_id = ng.id
		 JOIN plans p ON p.node_group_id = ng.id
		 JOIN nodes n ON n.id = c.exit_node_id
		 LEFT JOIN managed_entry_dns med ON med.node_id = c.entry_node_id
		   AND med.desired_action = 'present' AND med.cleanup_requested_at IS NULL
		 LEFT JOIN LATERAL (
		   SELECT COALESCE(array_agg(port ORDER BY port) FILTER (WHERE protocol = 'vless_reality'),
		     CASE WHEN count(*) = 0 THEN ARRAY[n.port]::integer[] ELSE ARRAY[]::integer[] END) AS ports
		   FROM inbounds WHERE node_id = n.id AND enabled = true
		 ) realityib ON true
		 LEFT JOIN LATERAL (
		     SELECT port FROM inbounds
		     WHERE node_id = n.id AND protocol = 'vmess_ws' AND enabled = true
		     ORDER BY created_at LIMIT 1
		 ) vmessib ON true
		 LEFT JOIN LATERAL (
		     SELECT port FROM inbounds
		     WHERE node_id = n.id AND protocol = 'trojan_tls' AND enabled = true
		     ORDER BY created_at LIMIT 1
		 ) trojanib ON true
		 LEFT JOIN LATERAL (
		     SELECT port, settings FROM inbounds
		     WHERE node_id = n.id AND protocol = 'shadowsocks' AND enabled = true
		     ORDER BY created_at LIMIT 1
		 ) ssib ON true
		 LEFT JOIN LATERAL (
		     SELECT port, settings FROM inbounds
		     WHERE node_id = n.id AND protocol = 'wireguard' AND enabled = true
		     ORDER BY created_at LIMIT 1
		 ) wgib ON true
		 LEFT JOIN LATERAL (
		     SELECT port FROM inbounds
		     WHERE node_id = n.id AND protocol = 'hysteria2' AND enabled = true
		     ORDER BY created_at LIMIT 1
		 ) hy2ib ON true
		 LEFT JOIN node_labels nl ON nl.node_id = n.id AND nl.language = $2
		 WHERE p.id = $1 AND n.status != 'pending' AND n.role IN ('exit','both')
		   AND c.enabled = true
		   AND (c.entry_node_id IS NOT NULL OR c.relay_pool_id IS NOT NULL OR n.publish_direct = true)
		   AND (c.entry_node_id IS NULL OR EXISTS (
		     SELECT 1 FROM nodes entry WHERE entry.id = c.entry_node_id
		       AND entry.role IN ('relay', 'both') AND family(entry.ip) = 4
		   ))
		   AND (c.relay_pool_id IS NULL OR EXISTS (
		     SELECT 1 FROM node_group_nodes ngn JOIN nodes relay ON relay.id = ngn.node_id
		     WHERE ngn.node_group_id = c.relay_pool_id AND relay.role IN ('relay','both') AND family(relay.ip) = 4
		   ))
		   AND ((c.entry_node_id IS NULL AND c.relay_pool_id IS NULL) OR c.health <> 'unhealthy')
		 ORDER BY c.priority DESC, n.name ASC`,
		user.PlanID, user.Language,
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
			&node.TLSCertFile, &node.TLSKeyFile,
			&node.VmessPort, &node.TrojanPort, &node.SSPort, &node.SSPassword,
			&node.WGPort, &node.WGServerPrivateKey,
			&node.HY2Port,
			&node.EntryHost, &node.EntryPort, &node.ChainExitPort,
			&node.RealityPorts, &node.RealitySNI, &node.RealityStatus,
			&node.ConfigHash, &node.XrayRunning, &node.HeartbeatFresh, &node.DeviceBandwidthReady,
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to read nodes",
			})
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read nodes",
		})
	}
	rows.Close()
	realityService := services.NewXrayConfigService(h.db)
	for i := range nodes {
		nodes[i].RealitySNI, nodes[i].GeneratedRealityPorts, nodes[i].RealityReady = realityApplied(ctx, h.db, realityService, nodes[i])
	}

	// Limited devices are VLESS-only. Their authenticated identity selects a
	// central-budget egress path on every Exit; legacy tier endpoints remain.
	speedMbps := 0
	if user.SpeedLimit != nil && *user.SpeedLimit > 0 {
		speedMbps = int(*user.SpeedLimit)
	}

	format := resolveSubscriptionFormat(c, formatOverride)
	if format == formatHTML {
		return renderSubscriptionPage(c, user)
	}

	var body []byte
	var contentType string

	switch format {
	case formatClashMeta, formatClash:
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps, deviceWGPrivateKey, deviceWGAddress)
		if format == formatClash {
			nodeInfos = subscription.LegacyClashCompatible(nodeInfos)
		}
		// A Clash profile without proxies is invalid, and nodes are withheld
		// until they apply the current config (e.g. right after a new UUID is
		// issued). Tell the client to retry instead of reporting a server fault.
		if len(nodeInfos) == 0 {
			return noReadyServers(c)
		}
		infoLabels := buildPlanInfoLabels(user)
		result, err := subscription.GenerateClash(nodeInfos, deviceUUID, infoLabels)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate clash config"})
		}
		body = result
		contentType = "text/yaml; charset=utf-8"

	case formatSingbox:
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps, deviceWGPrivateKey, deviceWGAddress)
		if len(nodeInfos) == 0 {
			return noReadyServers(c)
		}
		result, err := subscription.GenerateSingbox(nodeInfos, deviceUUID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate singbox config"})
		}
		body = result
		contentType = "application/json; charset=utf-8"

	case formatSurfboard:
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps, deviceWGPrivateKey, deviceWGAddress)
		result, err := subscription.GenerateSurfboard(nodeInfos, deviceUUID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate surfboard config"})
		}
		body = result
		contentType = "text/plain; charset=utf-8"

	case formatQuantumult:
		nodeInfos := buildNodeInfoList(nodes, deviceUUID, speedMbps, deviceWGPrivateKey, deviceWGAddress)
		result, err := subscription.GenerateQuantumult(nodeInfos, deviceUUID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate quantumult config"})
		}
		body = result
		contentType = "text/plain; charset=utf-8"

	case formatWireGuard:
		// Single-node wg-quick .conf for the plain WireGuard app, which can't
		// import Clash/Sing-box configs. Speed-limited plans are VLESS-only
		// (see buildNodeInfoList) so WireGuard isn't offered here either.
		if speedMbps > 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wireguard is not available on speed-limited plans"})
		}
		wgNode, ok := firstWireGuardNodeInfo(nodes, deviceWGPrivateKey, deviceWGAddress)
		if !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no wireguard-enabled node available"})
		}
		result, err := subscription.GenerateWireGuardConf(wgNode)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate wireguard config"})
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

			if !deviceBandwidthEligible(node, speedMbps) {
				continue
			}
			// An acknowledged per-device VLESS path keeps one endpoint per relay.
			if node.relayed() {
				if speedMbps > 0 && !slices.Contains(node.GeneratedRealityPorts, node.ChainExitPort) {
					continue
				}
				if link, ok := relayedLink(deviceUUID, node, fragment); ok {
					links = append(links, link)
				}
				continue
			}

			if node.RealityReady {
				ports := node.RealityPorts
				if speedMbps > 0 {
					port := speedtier.VlessPort(node.Port, speedMbps)
					ports = nil
					if slices.Contains(node.GeneratedRealityPorts, port) {
						ports = []int{port}
					}
				}
				for _, port := range ports {
					name := fragment
					if len(ports) > 1 {
						name = fmt.Sprintf("%s :%d", fragment, port)
					}
					links = append(links, buildVLESSLink(deviceUUID, node, port, name))
				}
			}

			// Speed-limited plans are VLESS-only (see buildNodeInfoList).
			if speedMbps > 0 {
				continue
			}

			hasTLS := node.TLSCertFile != nil && node.TLSKeyFile != nil &&
				*node.TLSCertFile != "" && *node.TLSKeyFile != ""

			if hasTLS && node.VmessPort != nil {
				links = append(links, buildVMESSLink(deviceUUID, node, *node.VmessPort, fragment))
			}
			if hasTLS && node.TrojanPort != nil {
				links = append(links, buildTrojanLink(deviceUUID, node, *node.TrojanPort, fragment))
			}

			if node.SSPassword != nil && *node.SSPassword != "" && node.SSPort != nil {
				links = append(links, buildSSLink(*node.SSPassword, node, *node.SSPort, fragment))
			}
		}
		body = []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n"))))
		contentType = "text/plain; charset=utf-8"
	}

	updateInterval := h.updateInterval
	var dbInterval int
	err = h.db.QueryRow(ctx,
		`SELECT value::int FROM settings WHERE key = 'subscription_update_interval'`,
	).Scan(&dbInterval)
	if err == nil && dbInterval > 0 {
		updateInterval = dbInterval
	}
	var profileTitle, supportURL string
	_ = h.db.QueryRow(ctx,
		`SELECT COALESCE((SELECT value FROM settings WHERE key = 'subscription_profile_title'), ''),
		        COALESCE((SELECT value FROM settings WHERE key = 'subscription_support_url'), '')`,
	).Scan(&profileTitle, &supportURL)
	if strings.TrimSpace(profileTitle) == "" {
		profileTitle = "Proxima VPN"
	}

	c.Set("Content-Type", contentType)
	setSubscriptionHeaders(c, user, subscriptionSettings{
		updateIntervalSeconds: updateInterval,
		profileTitle:          strings.TrimSpace(profileTitle),
		supportURL:            strings.TrimSpace(supportURL),
	})

	return c.Send(body)
}

func subscriptionEligibility(user subscriptionUser) (int, string) {
	if !user.IsActive || user.Status != "active" {
		return fiber.StatusForbidden, "subscription inactive"
	}
	if user.PlanExpiresAt != nil && user.PlanExpiresAt.Before(time.Now()) {
		return fiber.StatusForbidden, "plan expired"
	}
	if user.TrafficLimit != nil && *user.TrafficLimit > 0 && user.TrafficUsed >= *user.TrafficLimit {
		return fiber.StatusForbidden, "traffic limit exceeded"
	}
	return 0, ""
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

// chainProtocol names the protocol a relayed chain carries, found by matching the
// exit port the relay forwards to against that exit's listening ports. A chain
// forwards one port, so it is exactly one protocol - unlike a direct entry, which
// offers every protocol the exit runs.
func chainProtocol(node subscriptionNode) (string, bool) {
	switch {
	case slices.Contains(node.RealityPorts, node.ChainExitPort):
		return "vless_reality", true
	case node.VmessPort != nil && node.ChainExitPort == *node.VmessPort:
		return "vmess_ws", true
	case node.TrojanPort != nil && node.ChainExitPort == *node.TrojanPort:
		return "trojan_tls", true
	case node.SSPort != nil && node.ChainExitPort == *node.SSPort:
		return "shadowsocks", true
	case node.HY2Port != nil && node.ChainExitPort == *node.HY2Port:
		return "hysteria2", true
	case node.WGPort != nil && node.ChainExitPort == *node.WGPort:
		return "wireguard", true
	}
	return "", false
}

// relayedNodeInfo renders the single entry a relayed chain represents. Every
// credential comes from the exit, because the relay only rewrites a destination
// and never terminates the tunnel - which is what lets one chain shape carry
// Reality, Hysteria2 or WireGuard alike. Only the address is the relay's.
//
// ok is false when the chain names an exit port that no longer has an inbound, or
// when the device lacks the keys a protocol needs: advertising an endpoint that
// cannot authenticate is worse than omitting it.
func relayedNodeInfo(node subscriptionNode, deviceWGPrivateKey, deviceWGAddress *string) (subscription.NodeInfo, bool) {
	if node.dialHost() == "" {
		return subscription.NodeInfo{}, false
	}
	protocol, ok := chainProtocol(node)
	if !ok {
		return subscription.NodeInfo{}, false
	}

	info := subscription.NodeInfo{
		Name:     node.Name,
		IP:       node.dialHost(),
		Port:     *node.EntryPort,
		Protocol: protocol,
	}

	switch protocol {
	case "vless_reality":
		if !node.RealityReady {
			return subscription.NodeInfo{}, false
		}
		info.RealityPublicKey = node.RealityPublicKey
		info.RealityShortID = node.RealityShortID
		info.ServerName = node.RealitySNI
	case "vmess_ws":
		info.WSPath = "/vmess"
		info.TLSEnabled = true
		// The certificate has to cover the name the client dials, which is the
		// entry host rather than the exit - see the wildcard requirement in
		// docs; a per-exit certificate would not match.
		info.ServerName = node.dialHost()
	case "trojan_tls":
		info.TLSEnabled = true
		info.ServerName = node.dialHost()
	case "shadowsocks":
		if node.SSPassword == nil || *node.SSPassword == "" {
			return subscription.NodeInfo{}, false
		}
		info.SSMethod = "2022-blake3-aes-128-gcm"
		info.SSPassword = *node.SSPassword
	case "wireguard":
		if node.WGServerPrivateKey == nil || *node.WGServerPrivateKey == "" ||
			deviceWGPrivateKey == nil || *deviceWGPrivateKey == "" ||
			deviceWGAddress == nil || *deviceWGAddress == "" {
			return subscription.NodeInfo{}, false
		}
		serverPubKey, err := crypto.DeriveWireGuardPublicKey(*node.WGServerPrivateKey)
		if err != nil {
			return subscription.NodeInfo{}, false
		}
		info.WGPrivateKey = *deviceWGPrivateKey
		info.WGPeerPublicKey = serverPubKey
		info.WGAddress = *deviceWGAddress
	}

	return info, true
}

func deviceBandwidthEligible(node subscriptionNode, speedMbps int) bool {
	return speedMbps <= 0 || (node.DeviceBandwidthReady && node.RealityReady)
}

func buildNodeInfoList(nodes []subscriptionNode, userUUID string, speedMbps int, deviceWGPrivateKey, deviceWGAddress *string) []subscription.NodeInfo {
	var infos []subscription.NodeInfo
	for _, node := range nodes {
		// Never advertise a hard limit from a legacy/shared-tier or unacknowledged
		// node. Runtime identity routing and current canonical config are required.
		if !deviceBandwidthEligible(node, speedMbps) {
			continue
		}
		if node.relayed() {
			if speedMbps > 0 && !slices.Contains(node.GeneratedRealityPorts, node.ChainExitPort) {
				continue
			}
			if info, ok := relayedNodeInfo(node, deviceWGPrivateKey, deviceWGAddress); ok {
				infos = append(infos, info)
			}
			continue
		}

		if node.RealityReady {
			ports := node.RealityPorts
			if speedMbps > 0 {
				port := speedtier.VlessPort(node.Port, speedMbps)
				ports = nil
				if slices.Contains(node.GeneratedRealityPorts, port) {
					ports = []int{port}
				}
			}
			for _, port := range ports {
				name := node.Name
				if len(ports) > 1 {
					name = fmt.Sprintf("%s :%d", node.Name, port)
				}
				infos = append(infos, subscription.NodeInfo{
					Name: name, IP: node.IP, Port: port, Protocol: "vless_reality",
					RealityPublicKey: node.RealityPublicKey, RealityShortID: node.RealityShortID,
					ServerName: node.RealitySNI,
				})
			}
		}

		// Limited devices remain VLESS-only: other engines do not share this
		// authenticated per-device egress interface.
		if speedMbps > 0 {
			continue
		}

		hasTLS := node.TLSCertFile != nil && node.TLSKeyFile != nil &&
			*node.TLSCertFile != "" && *node.TLSKeyFile != ""

		if hasTLS && node.VmessPort != nil {
			infos = append(infos, subscription.NodeInfo{
				Name:       node.Name + " VMess",
				IP:         node.IP,
				Port:       *node.VmessPort,
				Protocol:   "vmess_ws",
				WSPath:     "/vmess",
				TLSEnabled: true,
				ServerName: node.IP,
			})
		}

		if hasTLS && node.TrojanPort != nil {
			infos = append(infos, subscription.NodeInfo{
				Name:       node.Name + " Trojan",
				IP:         node.IP,
				Port:       *node.TrojanPort,
				Protocol:   "trojan_tls",
				TLSEnabled: true,
				ServerName: node.IP,
			})
		}

		if node.SSPassword != nil && *node.SSPassword != "" && node.SSPort != nil {
			infos = append(infos, subscription.NodeInfo{
				Name:       node.Name + " SS",
				IP:         node.IP,
				Port:       *node.SSPort,
				Protocol:   "shadowsocks",
				SSMethod:   "2022-blake3-aes-128-gcm",
				SSPassword: *node.SSPassword,
			})
		}

		if node.HY2Port != nil {
			infos = append(infos, subscription.NodeInfo{
				Name:     node.Name + " Hysteria2",
				IP:       node.IP,
				Port:     *node.HY2Port,
				Protocol: "hysteria2",
			})
		}

		if node.WGPort != nil && node.WGServerPrivateKey != nil && *node.WGServerPrivateKey != "" &&
			deviceWGPrivateKey != nil && *deviceWGPrivateKey != "" &&
			deviceWGAddress != nil && *deviceWGAddress != "" {
			if serverPubKey, err := crypto.DeriveWireGuardPublicKey(*node.WGServerPrivateKey); err == nil {
				infos = append(infos, subscription.NodeInfo{
					Name:            node.Name + " WireGuard",
					IP:              node.IP,
					Port:            *node.WGPort,
					Protocol:        "wireguard",
					WGPrivateKey:    *deviceWGPrivateKey,
					WGPeerPublicKey: serverPubKey,
					WGAddress:       *deviceWGAddress,
				})
			}
		}
	}
	return infos
}

// firstWireGuardNodeInfo returns a standalone wireguard NodeInfo for the
// first node in nodes that has an enabled wireguard inbound, for the
// single-node format=wireguard .conf download (see GetSubscription).
func firstWireGuardNodeInfo(nodes []subscriptionNode, deviceWGPrivateKey, deviceWGAddress *string) (subscription.NodeInfo, bool) {
	if deviceWGPrivateKey == nil || *deviceWGPrivateKey == "" || deviceWGAddress == nil || *deviceWGAddress == "" {
		return subscription.NodeInfo{}, false
	}
	for _, node := range nodes {
		if node.WGPort == nil || node.WGServerPrivateKey == nil || *node.WGServerPrivateKey == "" {
			continue
		}
		// A relayed chain forwards one port, so it offers WireGuard only when that
		// is the port it forwards; the client then dials the chain's address.
		port := *node.WGPort
		if node.relayed() {
			if node.dialHost() == "" {
				continue
			}
			if protocol, ok := chainProtocol(node); !ok || protocol != "wireguard" {
				continue
			}
			port = *node.EntryPort
		}
		serverPubKey, err := crypto.DeriveWireGuardPublicKey(*node.WGServerPrivateKey)
		if err != nil {
			continue
		}
		return subscription.NodeInfo{
			Name:            node.Name + " WireGuard",
			IP:              node.dialHost(),
			Port:            port,
			Protocol:        "wireguard",
			WGPrivateKey:    *deviceWGPrivateKey,
			WGPeerPublicKey: serverPubKey,
			WGAddress:       *deviceWGAddress,
		}, true
	}
	return subscription.NodeInfo{}, false
}

// relayedLink renders the one link a relayed chain represents. Hysteria2 and
// WireGuard are absent because this format carries neither, which is why they are
// only offered through Clash and Sing-box.
func relayedLink(uuid string, node subscriptionNode, fragment string) (string, bool) {
	if node.dialHost() == "" {
		return "", false
	}
	protocol, ok := chainProtocol(node)
	if !ok {
		return "", false
	}

	port := *node.EntryPort
	switch protocol {
	case "vless_reality":
		if !node.RealityReady {
			return "", false
		}
		return buildVLESSLink(uuid, node, port, fragment), true
	case "vmess_ws":
		return buildVMESSLink(uuid, node, port, fragment), true
	case "trojan_tls":
		return buildTrojanLink(uuid, node, port, fragment), true
	case "shadowsocks":
		if node.SSPassword == nil || *node.SSPassword == "" {
			return "", false
		}
		return buildSSLink(*node.SSPassword, node, port, fragment), true
	}
	return "", false
}

func buildVLESSLink(uuid string, node subscriptionNode, port int, fragment string) string {
	return fmt.Sprintf(
		"vless://%s@%s:%d?type=tcp&security=reality&sni=%s&fp=chrome&pbk=%s&sid=%s&flow=xtls-rprx-vision#%s",
		uuid,
		node.dialHost(),
		port,
		url.QueryEscape(node.RealitySNI),
		url.QueryEscape(node.RealityPublicKey),
		url.QueryEscape(node.RealityShortID),
		url.PathEscape(fragment+" VLESS"),
	)
}

func buildVMESSLink(uuid string, node subscriptionNode, port int, fragment string) string {
	vmessConfig := map[string]interface{}{
		"v":    "2",
		"ps":   fragment + " VMess",
		"add":  node.dialHost(),
		"port": port,
		"id":   uuid,
		"aid":  0,
		"net":  "ws",
		"type": "none",
		"host": node.dialHost(),
		"path": "/vmess",
		"tls":  "tls",
	}
	jsonBytes, _ := json.Marshal(vmessConfig)
	return "vmess://" + base64.StdEncoding.EncodeToString(jsonBytes)
}

func buildTrojanLink(uuid string, node subscriptionNode, port int, fragment string) string {
	return fmt.Sprintf(
		"trojan://%s@%s:%d?security=tls&type=tcp#%s",
		uuid,
		node.dialHost(),
		port,
		url.PathEscape(fragment+" Trojan"),
	)
}

func buildSSLink(password string, node subscriptionNode, port int, fragment string) string {
	method := "2022-blake3-aes-128-gcm"
	userinfo := base64.URLEncoding.EncodeToString([]byte(method + ":" + password))
	return fmt.Sprintf(
		"ss://%s@%s:%d#%s",
		userinfo,
		node.dialHost(),
		port,
		url.PathEscape(fragment+" SS"),
	)
}
