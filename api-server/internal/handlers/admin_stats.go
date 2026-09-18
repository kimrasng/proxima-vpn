package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// AdminStatsHandler handles admin dashboard statistics.
type AdminStatsHandler struct {
	db      *pgxpool.Pool
	tracker *services.OnlineTracker
	stats   *services.StatsService
}

// NewAdminStatsHandler creates a new AdminStatsHandler.
func NewAdminStatsHandler(db *pgxpool.Pool, tracker *services.OnlineTracker) *AdminStatsHandler {
	return &AdminStatsHandler{db: db, tracker: tracker, stats: services.NewStatsService(db)}
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

	var totalTrafficToday, totalTrafficMonth int64

	// up_bytes + dn_bytes: traffic_logs has no single "bytes" column (see
	// database/schema.go). The previous version of this query referenced
	// SUM(bytes), which errored on every call; the error was discarded (the
	// call used `_ =`), so these two figures were silently always 0.
	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(up_bytes + dn_bytes), 0) FROM traffic_logs
		WHERE created_at >= CURRENT_DATE
	`).Scan(&totalTrafficToday); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(up_bytes + dn_bytes), 0) FROM traffic_logs
		WHERE created_at >= DATE_TRUNC('month', CURRENT_DATE)
	`).Scan(&totalTrafficMonth); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	onlineUsers, _ := h.tracker.GetAllOnlineCount(ctx)

	return c.JSON(fiber.Map{
		"total_users":         summary.TotalUsers,
		"active_users":        summary.ActiveUsers,
		"online_users":        onlineUsers,
		"total_nodes":         summary.TotalNodes,
		"online_nodes":        summary.OnlineNodes,
		"total_traffic_today": totalTrafficToday,
		"total_traffic_month": totalTrafficMonth,
		"pending_requests":    pendingRequests,
	})
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

	type onlineUserResponse struct {
		Email    string `json:"email"`
		Device   string `json:"device"`
		NodeName string `json:"node_name"`
		// The cap applies to distinct source addresses across the user's whole
		// pool, so it is reported per user rather than per row.
		OnlineIPs     int  `json:"online_ips"`
		MaxConcurrent int  `json:"max_concurrent"`
		OverCap       bool `json:"over_cap"`
	}

	type deviceInfo struct {
		email  string
		device string
		userID string
	}

	deviceRows, err := h.db.Query(ctx, `
		SELECT d.xray_uuid, COALESCE(d.name, 'Unknown'), u.email, u.id::text
		FROM devices d
		JOIN users u ON u.id = d.user_id
		WHERE d.xray_uuid = ANY($1)
	`, uuids)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query device info"})
	}
	defer deviceRows.Close()

	deviceMap := make(map[string]deviceInfo)
	for deviceRows.Next() {
		var uuid, device, email, userID string
		if err := deviceRows.Scan(&uuid, &device, &email, &userID); err != nil {
			continue
		}
		deviceMap[uuid] = deviceInfo{email: email, device: device, userID: userID}
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
		ips, _, err := h.tracker.CountDistinctIPsForUser(ctx, h.db, info.userID)
		if err != nil {
			ips = 0
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
		result = append(result, onlineUserResponse{
			Email:         info.email,
			Device:        info.device,
			NodeName:      nodeName,
			OnlineIPs:     uc.onlineIPs,
			MaxConcurrent: uc.maxConc,
			OverCap:       uc.maxConc > 0 && uc.onlineIPs > uc.maxConc,
		})
	}

	return c.JSON(result)
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
