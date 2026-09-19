package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// AdminStatsHandler handles admin dashboard statistics.
type AdminStatsHandler struct {
	db       *pgxpool.Pool
	tracker  *services.OnlineTracker
	stats    *services.StatsService
	activity *services.ActivityService
}

// NewAdminStatsHandler creates a new AdminStatsHandler.
func NewAdminStatsHandler(db *pgxpool.Pool, tracker *services.OnlineTracker) *AdminStatsHandler {
	return &AdminStatsHandler{
		db:       db,
		tracker:  tracker,
		stats:    services.NewStatsService(db),
		activity: services.NewActivityService(db),
	}
}

// deltaBaselineAge keeps KPI deltas meaning "since yesterday" even for a panel
// that was left open overnight.
const deltaBaselineAge = 24 * time.Hour

type dashboardDeltas struct {
	Available    bool  `json:"available"`
	ActiveAlerts int   `json:"active_alerts"`
	OnlineNodes  int   `json:"online_nodes"`
	OnlineUsers  int   `json:"online_users"`
	TotalUsers   int   `json:"total_users"`
	TrafficToday int64 `json:"traffic_today"`
}

// GetDashboardStats returns aggregate statistics for the admin dashboard.
// @Summary Get dashboard stats
// @Description Returns aggregate statistics for the admin dashboard
// @Tags admin-stats
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/stats [get]
func (h *AdminStatsHandler) GetDashboardStats(c *fiber.Ctx) error {
	ctx := context.Background()

	summary, err := h.stats.GetSummary(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	var pendingRequests int64
	err = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM plan_requests WHERE status = 'pending'`).Scan(&pendingRequests)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	var uploadToday, downloadToday, totalTrafficMonth int64

	// up_bytes + dn_bytes: traffic_logs has no single "bytes" column (see
	// database/schema.go). The previous version of this query referenced
	// SUM(bytes), which errored on every call; the error was discarded (the
	// call used `_ =`), so these two figures were silently always 0.
	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(up_bytes), 0), COALESCE(SUM(dn_bytes), 0) FROM traffic_logs
		WHERE created_at >= CURRENT_DATE
	`).Scan(&uploadToday, &downloadToday); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(up_bytes + dn_bytes), 0) FROM traffic_logs
		WHERE created_at >= DATE_TRUNC('month', CURRENT_DATE)
	`).Scan(&totalTrafficMonth); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	trafficToday := uploadToday + downloadToday
	onlineUsers, _ := h.tracker.GetAllOnlineCount(ctx)

	alerts, err := h.stats.GetAlerts(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	deltas := dashboardDeltas{}
	if baseline, ok, err := h.stats.GetBaseline(ctx, deltaBaselineAge); err == nil && ok {
		deltas = dashboardDeltas{
			Available:    true,
			ActiveAlerts: alerts.Total - baseline.ActiveAlerts,
			OnlineNodes:  int(summary.OnlineNodes) - baseline.OnlineNodes,
			OnlineUsers:  onlineUsers - baseline.OnlineUsers,
			TotalUsers:   int(summary.TotalUsers) - baseline.TotalUsers,
			TrafficToday: trafficToday - baseline.TrafficToday,
		}
	}

	return c.JSON(fiber.Map{
		"total_users":         summary.TotalUsers,
		"active_users":        summary.ActiveUsers,
		"online_users":        onlineUsers,
		"total_nodes":         summary.TotalNodes,
		"online_nodes":        summary.OnlineNodes,
		"total_traffic_today": trafficToday,
		"upload_today":        uploadToday,
		"download_today":      downloadToday,
		"total_traffic_month": totalTrafficMonth,
		"pending_requests":    pendingRequests,
		"active_alerts":       alerts.Total,
		"deltas":              deltas,
		"generated_at":        time.Now().UTC().Format(time.RFC3339),
	})
}

// GetAlerts returns the conditions needing operator action.
// @Summary Get dashboard alerts
// @Description Returns offline nodes, resource-pressured nodes, and pending approvals
// @Tags admin-stats
// @Produce json
// @Success 200 {object} services.Alerts
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/stats/alerts [get]
func (h *AdminStatsHandler) GetAlerts(c *fiber.Ctx) error {
	alerts, err := h.stats.GetAlerts(context.Background())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query alerts"})
	}
	return c.JSON(alerts)
}

// GetNodeTraffic returns traffic broken down by node.
// @Summary Get per-node traffic
// @Description Returns upload/download per node over the requested window
// @Tags admin-stats
// @Produce json
// @Param window query string false "today, week, or month (default today)"
// @Param limit query int false "Maximum nodes to return (default 10)"
// @Success 200 {array} services.NodeTraffic
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/stats/node-traffic [get]
func (h *AdminStatsHandler) GetNodeTraffic(c *fiber.Ctx) error {
	window := services.ParseTrafficWindow(c.Query("window"))
	limit := c.QueryInt("limit", 10)

	traffic, err := h.stats.GetNodeTraffic(context.Background(), window, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query node traffic"})
	}
	return c.JSON(traffic)
}

// GetActivity returns the recent activity feed.
// @Summary Get recent activity
// @Description Returns the most recent recorded panel events, newest first. Supplying both target_type and target_id narrows the feed to that target.
// @Tags admin-stats
// @Produce json
// @Param limit query int false "Maximum entries to return (default 20)"
// @Param target_type query string false "Restrict to one target type, e.g. node (requires target_id)"
// @Param target_id query string false "Restrict to one target id (requires target_type)"
// @Success 200 {array} services.Entry
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/activity [get]
func (h *AdminStatsHandler) GetActivity(c *fiber.Ctx) error {
	entries, err := h.activity.ListForTarget(
		context.Background(),
		c.QueryInt("limit", 20),
		c.Query("target_type"),
		c.Query("target_id"),
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query activity"})
	}
	return c.JSON(entries)
}

// GetOnlineUsers returns detailed info about currently connected users.
// @Summary Get online users
// @Description Returns detailed info about currently connected users
// @Tags admin-stats
// @Produce json
// @Success 200 {array} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/online-users [get]
func (h *AdminStatsHandler) GetOnlineUsers(c *fiber.Ctx) error {
	ctx := context.Background()

	uuidToNode, err := h.tracker.GetAllOnlineUUIDs(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query online users"})
	}

	if len(uuidToNode) == 0 {
		return c.JSON([]interface{}{})
	}

	uuids := make([]string, 0, len(uuidToNode))
	for uuid := range uuidToNode {
		uuids = append(uuids, uuid)
	}

	type onlineAddress struct {
		IP       string `json:"ip"`
		LastSeen string `json:"last_seen"`
	}

	type onlineUserResponse struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		DeviceID string `json:"device_id"`
		Device   string `json:"device"`
		NodeID   string `json:"node_id"`
		NodeName string `json:"node_name"`
		// Where this specific device is connected from. The cap below counts the
		// user's whole pool, so this is the only per-device address view.
		Addresses []onlineAddress `json:"addresses"`
		// The cap applies to distinct source addresses across the user's whole
		// pool, so it is reported per user rather than per row.
		OnlineIPs     int  `json:"online_ips"`
		MaxConcurrent int  `json:"max_concurrent"`
		OverCap       bool `json:"over_cap"`
		// Null when the session predates the stamp the node agent writes, which
		// the UI renders as unknown rather than as a just-opened connection.
		ConnectedSince *string `json:"connected_since"`
		TrafficToday   int64   `json:"traffic_today"`
	}

	type deviceInfo struct {
		id     string
		email  string
		name   string
		device string
		userID string
	}

	deviceRows, err := h.db.Query(ctx, `
		SELECT d.id::text, d.xray_uuid, COALESCE(d.name, 'Unknown'), u.email, u.name, u.id::text
		FROM devices d
		JOIN users u ON u.id = d.user_id
		WHERE d.xray_uuid = ANY($1)
	`, uuids)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query device info"})
	}
	defer deviceRows.Close()

	deviceMap := make(map[string]deviceInfo)
	deviceIDs := make([]string, 0, len(uuids))
	for deviceRows.Next() {
		var id, uuid, device, email, name, userID string
		if err := deviceRows.Scan(&id, &uuid, &device, &email, &name, &userID); err != nil {
			continue
		}
		deviceMap[uuid] = deviceInfo{id: id, email: email, name: name, device: device, userID: userID}
		deviceIDs = append(deviceIDs, id)
	}

	trafficToday := make(map[string]int64, len(deviceIDs))
	if len(deviceIDs) > 0 {
		trafficRows, err := h.db.Query(ctx, `
			SELECT device_id::text, COALESCE(SUM(up_bytes + dn_bytes), 0)
			FROM traffic_logs
			WHERE device_id = ANY($1) AND created_at >= CURRENT_DATE
			GROUP BY device_id
		`, deviceIDs)
		if err == nil {
			defer trafficRows.Close()
			for trafficRows.Next() {
				var deviceID string
				var bytes int64
				if err := trafficRows.Scan(&deviceID, &bytes); err != nil {
					continue
				}
				trafficToday[deviceID] = bytes
			}
		}
	}

	sessionStarts, err := h.tracker.GetSessionStarts(ctx, uuids)
	if err != nil {
		sessionStarts = map[string]int64{}
	}

	nodeIDs := make([]string, 0)
	nodeIDSet := make(map[string]struct{})
	for _, nodeID := range uuidToNode {
		if _, exists := nodeIDSet[nodeID]; !exists {
			nodeIDSet[nodeID] = struct{}{}
			nodeIDs = append(nodeIDs, nodeID)
		}
	}

	nodeNames := make(map[string]string)
	if len(nodeIDs) > 0 {
		nodeRows, err := h.db.Query(ctx, `SELECT id, name FROM nodes WHERE id = ANY($1)`, nodeIDs)
		if err == nil {
			defer nodeRows.Close()
			for nodeRows.Next() {
				var id, name string
				if err := nodeRows.Scan(&id, &name); err != nil {
					continue
				}
				nodeNames[id] = name
			}
		}
	}

	// Resolved once per user, not per row: a user with several devices online
	// shares one cap and one address count.
	type userCap struct {
		onlineIPs int
		maxConc   int
	}
	caps := map[string]userCap{}
	addressesByUUID := map[string][]onlineAddress{}
	for _, info := range deviceMap {
		if _, done := caps[info.userID]; done {
			continue
		}
		var maxConc int
		if err := h.db.QueryRow(ctx,
			`SELECT COALESCE(p.max_concurrent, p.max_devices, 0)
			 FROM users u LEFT JOIN plans p ON p.id = u.plan_id
			 WHERE u.id = $1`, info.userID,
		).Scan(&maxConc); err != nil {
			maxConc = 0
		}
		ips, perDevice, err := h.tracker.CountDistinctIPsForUser(ctx, h.db, info.userID)
		if err != nil {
			ips = 0
		}
		for uuid, byIP := range perDevice {
			list := make([]onlineAddress, 0, len(byIP))
			for ip, lastSeen := range byIP {
				list = append(list, onlineAddress{
					IP:       ip,
					LastSeen: time.Unix(lastSeen, 0).UTC().Format(time.RFC3339),
				})
			}
			sort.Slice(list, func(a, b int) bool { return list[a].IP < list[b].IP })
			addressesByUUID[uuid] = list
		}
		caps[info.userID] = userCap{onlineIPs: ips, maxConc: maxConc}
	}

	result := make([]onlineUserResponse, 0, len(uuidToNode))
	for uuid, nodeID := range uuidToNode {
		info, ok := deviceMap[uuid]
		if !ok {
			continue
		}
		nodeName := nodeNames[nodeID]
		if nodeName == "" {
			nodeName = "Unknown"
		}
		uc := caps[info.userID]

		var connectedSince *string
		if start, ok := sessionStarts[uuid]; ok {
			stamp := time.Unix(start, 0).UTC().Format(time.RFC3339)
			connectedSince = &stamp
		}

		addresses := addressesByUUID[uuid]
		if addresses == nil {
			addresses = []onlineAddress{}
		}

		result = append(result, onlineUserResponse{
			Email:          info.email,
			Name:           info.name,
			DeviceID:       info.id,
			Device:         info.device,
			NodeID:         nodeID,
			NodeName:       nodeName,
			Addresses:      addresses,
			OnlineIPs:      uc.onlineIPs,
			MaxConcurrent:  uc.maxConc,
			OverCap:        uc.maxConc > 0 && uc.onlineIPs > uc.maxConc,
			ConnectedSince: connectedSince,
			TrafficToday:   trafficToday[info.id],
		})
	}

	sort.Slice(result, func(a, b int) bool {
		if result[a].Email != result[b].Email {
			return result[a].Email < result[b].Email
		}
		return result[a].Device < result[b].Device
	})

	return c.JSON(result)
}

type terminateSessionRequest struct {
	CooldownMinutes int `json:"cooldown_minutes"`
}

// Withdrawing the credential is what ends the session, so it must stay
// withdrawn long enough for the client to stop retrying: a client that
// reconnects instantly gets re-provisioned on the next config poll, making the
// action look like it did nothing.
const (
	defaultTerminateCooldown = 10
	maxTerminateCooldown     = 24 * 60
)

// TerminateSession withdraws one device's credential so its live session drops.
//
// Nodes are polled, never pushed to, so this cannot sever a socket directly: it
// marks the device evicted, the agent stops receiving it in the config it polls
// every 30s, and the agent then drops the user from the running Xray. The
// session therefore ends within one poll interval rather than instantly.
// @Summary Terminate a device's session
// @Description Evicts a device so the node agent withdraws its credential on the next config poll
// @Tags admin-stats
// @Accept json
// @Produce json
// @Param id path string true "Device ID"
// @Param body body terminateSessionRequest false "Cooldown in minutes"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/devices/{id}/terminate [post]
func (h *AdminStatsHandler) TerminateSession(c *fiber.Ctx) error {
	ctx := context.Background()
	deviceID := c.Params("id")

	var req terminateSessionRequest
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
	}

	cooldown := req.CooldownMinutes
	if cooldown <= 0 {
		cooldown = defaultTerminateCooldown
	}
	if cooldown > maxTerminateCooldown {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "cooldown_minutes exceeds the maximum",
		})
	}

	var xrayUUID, email, deviceName string
	err := h.db.QueryRow(ctx, `
		SELECT d.xray_uuid, u.email, COALESCE(d.name, '')
		FROM devices d
		JOIN users u ON u.id = d.user_id
		WHERE d.id = $1
	`, deviceID).Scan(&xrayUUID, &email, &deviceName)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "device not found"})
	}

	if _, err := h.db.Exec(ctx,
		`UPDATE devices SET evicted_until = NOW() + make_interval(mins => $1) WHERE id = $2`,
		cooldown, deviceID,
	); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to terminate session",
		})
	}

	// Clears the cached online report so the row leaves the connections view now
	// instead of lingering until its TTL lapses.
	h.tracker.ForgetDevice(ctx, xrayUUID)

	adminLabel, _ := c.Locals("email").(string)
	adminID, _ := c.Locals("admin_id").(string)
	h.activity.Log(ctx, services.Record{
		EventType:  services.EventSessionTerminated,
		Severity:   services.SeverityWarning,
		ActorType:  "admin",
		ActorID:    adminID,
		ActorLabel: adminLabel,
		TargetType: "device",
		TargetID:   deviceID,
		Detail: map[string]any{
			"email":            email,
			"device":           deviceName,
			"cooldown_minutes": cooldown,
		},
	})

	return c.JSON(fiber.Map{
		"message":          "session terminated",
		"cooldown_minutes": cooldown,
	})
}

// GetTrafficHistory returns daily upload/download traffic for the last 7 days.
// @Summary Get traffic history
// @Description Returns daily upload/download traffic aggregated from traffic_logs
// @Tags admin-stats
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/stats/traffic-history [get]
func (h *AdminStatsHandler) GetTrafficHistory(c *fiber.Ctx) error {
	ctx := context.Background()

	rows, err := h.db.Query(ctx, `
		SELECT
			created_at::date AS day,
			COALESCE(SUM(up_bytes), 0) AS upload,
			COALESCE(SUM(dn_bytes), 0) AS download
		FROM traffic_logs
		WHERE created_at >= CURRENT_DATE - INTERVAL '6 days'
		GROUP BY day
		ORDER BY day ASC
	`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query traffic history"})
	}
	defer rows.Close()

	type trafficEntry struct {
		Date     string `json:"date"`
		Upload   int64  `json:"upload"`
		Download int64  `json:"download"`
	}

	result := make([]trafficEntry, 0, 7)
	for rows.Next() {
		var entry trafficEntry
		var day interface{}
		if err := rows.Scan(&day, &entry.Upload, &entry.Download); err != nil {
			continue
		}
		switch v := day.(type) {
		case time.Time:
			entry.Date = v.Format("2006-01-02")
		default:
			continue
		}
		result = append(result, entry)
	}

	return c.JSON(result)
}
