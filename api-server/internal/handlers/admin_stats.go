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
}

// NewAdminStatsHandler creates a new AdminStatsHandler.
func NewAdminStatsHandler(db *pgxpool.Pool, tracker *services.OnlineTracker) *AdminStatsHandler {
	return &AdminStatsHandler{db: db, tracker: tracker}
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

	var totalUsers, activeUsers, totalNodes, onlineNodes, pendingRequests int64

	err := h.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&totalUsers)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	err = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE status = 'active' AND is_active = true`).Scan(&activeUsers)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	err = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM nodes WHERE status != 'pending'`).Scan(&totalNodes)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	err = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM nodes WHERE status = 'online'`).Scan(&onlineNodes)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	err = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM plan_requests WHERE status = 'pending'`).Scan(&pendingRequests)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to query stats"})
	}

	var totalTrafficToday, totalTrafficMonth int64

	_ = h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(bytes), 0) FROM traffic_logs
		WHERE created_at >= CURRENT_DATE
	`).Scan(&totalTrafficToday)

	_ = h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(bytes), 0) FROM traffic_logs
		WHERE created_at >= DATE_TRUNC('month', CURRENT_DATE)
	`).Scan(&totalTrafficMonth)

	onlineUsers, _ := h.tracker.GetAllOnlineCount(ctx)

	return c.JSON(fiber.Map{
		"total_users":         totalUsers,
		"active_users":        activeUsers,
		"online_users":        onlineUsers,
		"total_nodes":         totalNodes,
		"online_nodes":        onlineNodes,
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
	}

	type deviceInfo struct {
		email  string
		device string
	}

	deviceRows, err := h.db.Query(ctx, `
		SELECT d.xray_uuid, COALESCE(d.name, 'Unknown'), u.email
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
		var uuid, device, email string
		if err := deviceRows.Scan(&uuid, &device, &email); err != nil {
			continue
		}
		deviceMap[uuid] = deviceInfo{email: email, device: device}
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
		result = append(result, onlineUserResponse{
			Email:    info.email,
			Device:   info.device,
			NodeName: nodeName,
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
