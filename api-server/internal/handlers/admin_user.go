package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// AdminUserHandler handles admin user management endpoints.
type AdminUserHandler struct {
	db     *pgxpool.Pool
	stats  *services.StatsService
	logins *services.LoginHistoryService
}

// NewAdminUserHandler creates a new AdminUserHandler.
func NewAdminUserHandler(db *pgxpool.Pool) *AdminUserHandler {
	return &AdminUserHandler{
		db:     db,
		stats:  services.NewStatsService(db),
		logins: services.NewLoginHistoryService(db),
	}
}

type userListItem struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	PlanID        *string    `json:"plan_id"`
	PlanName      *string    `json:"plan_name"`
	PlanExpiresAt *time.Time `json:"plan_expires_at"`
	TrafficUsed   int64      `json:"traffic_used"`
	TrafficLimit  *int64     `json:"traffic_limit"`
	IsActive      bool       `json:"is_active"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
}

type userListResponse struct {
	Users []userListItem `json:"users"`
	Total int            `json:"total"`
	Page  int            `json:"page"`
	Limit int            `json:"limit"`
}

type createUserRequest struct {
	Email    string  `json:"email"`
	Password string  `json:"password"`
	Name     string  `json:"name"`
	PlanID   *string `json:"plan_id"`
	IsActive *bool   `json:"is_active"`
}

type createUserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Create creates a new user as admin.
// @Summary Create user
// @Description Creates a new user account with active status
// @Tags admin-users
// @Accept json
// @Produce json
// @Param body body createUserRequest true "User details"
// @Success 201 {object} createUserResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users [post]
func (h *AdminUserHandler) Create(c *fiber.Ctx) error {
	var req createUserRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if req.Email == "" || req.Password == "" || req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email, password, and name are required"})
	}

	var exists bool
	err := h.db.QueryRow(
		context.Background(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`,
		req.Email,
	).Scan(&exists)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if exists {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already exists"})
	}

	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	subToken := crypto.NewUUID()

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	var resp createUserResponse
	err = h.db.QueryRow(
		context.Background(),
		`INSERT INTO users (email, password_hash, name, sub_token, status, is_active, plan_id)
		 VALUES ($1, $2, $3, $4, 'active', $5, $6)
		 RETURNING id, email, name, status, created_at`,
		req.Email, passwordHash, req.Name, subToken, isActive, req.PlanID,
	).Scan(&resp.ID, &resp.Email, &resp.Name, &resp.Status, &resp.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	return c.Status(fiber.StatusCreated).JSON(resp)
}

// List returns paginated users with optional search and status filter.
// @Summary List users
// @Description Returns paginated users with optional search and status filter
// @Tags admin-users
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param search query string false "Search by email or name"
// @Param status query string false "Filter by status"
// @Success 200 {object} userListResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users [get]
func (h *AdminUserHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	search := c.Query("search")
	status := c.Query("status")

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	where := []string{"1=1"}
	args := []interface{}{}
	argIdx := 1

	if search != "" {
		where = append(where, fmt.Sprintf("(u.email ILIKE $%d OR u.name ILIKE $%d)", argIdx, argIdx))
		args = append(args, "%"+search+"%")
		argIdx++
	}
	if status != "" {
		where = append(where, fmt.Sprintf("u.status = $%d", argIdx))
		args = append(args, status)
		argIdx++
	}

	whereClause := strings.Join(where, " AND ")

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM users u WHERE %s", whereClause)
	var total int
	if err := h.db.QueryRow(context.Background(), countQuery, args...).Scan(&total); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to count users",
		})
	}

	// Fetch users
	query := fmt.Sprintf(`
		SELECT u.id, u.email, u.name, u.plan_id, p.name, u.plan_expires_at,
		       u.traffic_used, p.traffic_limit, u.is_active, u.status, u.created_at
		FROM users u
		LEFT JOIN plans p ON u.plan_id = p.id
		WHERE %s
		ORDER BY u.created_at DESC
		LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1)

	queryArgs := append(args, limit, offset)

	rows, err := h.db.Query(context.Background(), query, queryArgs...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list users",
		})
	}
	defer rows.Close()

	users := make([]userListItem, 0)
	for rows.Next() {
		var u userListItem
		if err := rows.Scan(
			&u.ID, &u.Email, &u.Name, &u.PlanID, &u.PlanName, &u.PlanExpiresAt,
			&u.TrafficUsed, &u.TrafficLimit, &u.IsActive, &u.Status, &u.CreatedAt,
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan user",
			})
		}
		users = append(users, u)
	}

	return c.JSON(userListResponse{
		Users: users,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}

type deviceItem struct {
	ID        string    `json:"id"`
	Name      *string   `json:"name"`
	XrayUUID  string    `json:"xray_uuid"`
	CreatedAt time.Time `json:"created_at"`
}

type userDetailResponse struct {
	ID             string       `json:"id"`
	Email          string       `json:"email"`
	Name           string       `json:"name"`
	SubToken       string       `json:"sub_token"`
	PlanID         *string      `json:"plan_id"`
	PlanName       *string      `json:"plan_name"`
	PlanStartedAt  *time.Time   `json:"plan_started_at"`
	PlanExpiresAt  *time.Time   `json:"plan_expires_at"`
	TrafficUsed    int64        `json:"traffic_used"`
	TrafficLimit   *int64       `json:"traffic_limit"`
	TrafficResetAt *time.Time   `json:"traffic_reset_at"`
	IsActive       bool         `json:"is_active"`
	Status         string       `json:"status"`
	CreatedAt      time.Time    `json:"created_at"`
	Devices        []deviceItem `json:"devices"`
}

// Get returns a single user with plan and device details.
// @Summary Get user
// @Description Returns a single user with plan and device details
// @Tags admin-users
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} userDetailResponse
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id} [get]
func (h *AdminUserHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")

	var u userDetailResponse
	err := h.db.QueryRow(
		context.Background(),
		`SELECT u.id, u.email, u.name, u.sub_token, u.plan_id, p.name,
		        u.plan_started_at, u.plan_expires_at, u.traffic_used, p.traffic_limit,
		        u.traffic_reset_at, u.is_active, u.status, u.created_at
		 FROM users u
		 LEFT JOIN plans p ON u.plan_id = p.id
		 WHERE u.id = $1`,
		id,
	).Scan(
		&u.ID, &u.Email, &u.Name, &u.SubToken, &u.PlanID, &u.PlanName,
		&u.PlanStartedAt, &u.PlanExpiresAt, &u.TrafficUsed, &u.TrafficLimit,
		&u.TrafficResetAt, &u.IsActive, &u.Status, &u.CreatedAt,
	)
	if err != nil {
		// Only an absent row is a 404. Reporting every failure that way is what
		// made a query naming a non-existent column look like a missing user.
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "user not found",
			})
		}
		log.Printf("admin get user %s: %v", id, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch user",
		})
	}

	// Fetch devices
	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, name, xray_uuid, created_at FROM devices WHERE user_id = $1 ORDER BY created_at`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch devices",
		})
	}
	defer rows.Close()

	devices := make([]deviceItem, 0)
	for rows.Next() {
		var d deviceItem
		if err := rows.Scan(&d.ID, &d.Name, &d.XrayUUID, &d.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan device",
			})
		}
		devices = append(devices, d)
	}
	u.Devices = devices

	return c.JSON(u)
}

type updateUserRequest struct {
	Name          *string    `json:"name"`
	Status        *string    `json:"status"`
	IsActive      *bool      `json:"is_active"`
	PlanID        *string    `json:"plan_id"`
	PlanStartedAt *time.Time `json:"plan_started_at"`
	PlanExpiresAt *time.Time `json:"plan_expires_at"`
}

// Update partially updates a user's status, plan, or active state.
// @Summary Update user
// @Description Partially updates a user's status, plan, or active state
// @Tags admin-users
// @Accept json
// @Produce json
// @Param id path string true "User ID"
// @Param body body updateUserRequest true "Fields to update"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id} [put]
func (h *AdminUserHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
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
	if req.Status != nil {
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, *req.Status)
		argIdx++
	}
	if req.IsActive != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_active = $%d", argIdx))
		args = append(args, *req.IsActive)
		argIdx++
	}
	if req.PlanID != nil {
		setClauses = append(setClauses, fmt.Sprintf("plan_id = $%d", argIdx))
		args = append(args, *req.PlanID)
		argIdx++
	}
	if req.PlanStartedAt != nil {
		setClauses = append(setClauses, fmt.Sprintf("plan_started_at = $%d", argIdx))
		args = append(args, *req.PlanStartedAt)
		argIdx++
	}
	if req.PlanExpiresAt != nil {
		setClauses = append(setClauses, fmt.Sprintf("plan_expires_at = $%d", argIdx))
		args = append(args, *req.PlanExpiresAt)
		argIdx++
	}

	if len(setClauses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no fields to update",
		})
	}

	query := fmt.Sprintf(
		"UPDATE users SET %s WHERE id = $%d RETURNING id, email, name, plan_id, plan_started_at, plan_expires_at, traffic_used, is_active, status, created_at",
		strings.Join(setClauses, ", "), argIdx,
	)
	args = append(args, id)

	var u struct {
		ID            string     `json:"id"`
		Email         string     `json:"email"`
		Name          string     `json:"name"`
		PlanID        *string    `json:"plan_id"`
		PlanStartedAt *time.Time `json:"plan_started_at"`
		PlanExpiresAt *time.Time `json:"plan_expires_at"`
		TrafficUsed   int64      `json:"traffic_used"`
		IsActive      bool       `json:"is_active"`
		Status        string     `json:"status"`
		CreatedAt     time.Time  `json:"created_at"`
	}

	err := h.db.QueryRow(context.Background(), query, args...).Scan(
		&u.ID, &u.Email, &u.Name, &u.PlanID, &u.PlanStartedAt,
		&u.PlanExpiresAt, &u.TrafficUsed, &u.IsActive, &u.Status, &u.CreatedAt,
	)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user not found",
		})
	}

	return c.JSON(u)
}

// Delete soft-deactivates a user by setting is_active=false and status='suspended'.
// @Summary Delete user
// @Description Soft-deactivates a user (sets status to suspended)
// @Tags admin-users
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id} [delete]
func (h *AdminUserHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	result, err := h.db.Exec(
		context.Background(),
		`UPDATE users SET is_active = false, status = 'suspended' WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to deactivate user",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user not found",
		})
	}

	return c.JSON(fiber.Map{"message": "user deactivated"})
}

// ResetTraffic resets a user's traffic_used to 0 and updates traffic_reset_at.
// @Summary Reset user traffic
// @Description Resets traffic_used to 0 and sets traffic_reset_at to NOW()
// @Tags admin-users
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id}/reset-traffic [post]
func (h *AdminUserHandler) ResetTraffic(c *fiber.Ctx) error {
	id := c.Params("id")

	result, err := h.db.Exec(
		context.Background(),
		`UPDATE users SET traffic_used = 0, traffic_reset_at = NOW(), updated_at = NOW() WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to reset traffic",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user not found",
		})
	}

	return c.JSON(fiber.Map{"message": "traffic reset"})
}

// isUUID guards the id-taking read endpoints. Handing a non-UUID path segment
// straight to a uuid comparison makes Postgres reject the query, which the
// handler could only report as a 500 for what is really a malformed request.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool {
	return uuidPattern.MatchString(s)
}

type userTrafficResponse struct {
	TrafficUsed      int64                      `json:"traffic_used"`
	TrafficLimit     *int64                     `json:"traffic_limit"`
	TrafficRemaining *int64                     `json:"traffic_remaining"`
	Percentage       float64                    `json:"percentage"`
	Unlimited        bool                       `json:"unlimited"`
	Window           string                     `json:"window"`
	ByNode           []services.UserNodeTraffic `json:"by_node"`
}

// GetTraffic returns a user's quota position and a per-node usage breakdown.
// @Summary Get user traffic
// @Description Returns quota, usage, remaining traffic and a per-node breakdown for one user
// @Tags admin-users
// @Produce json
// @Param id path string true "User ID"
// @Param window query string false "today, week, or month (default month)"
// @Success 200 {object} userTrafficResponse
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id}/traffic [get]
func (h *AdminUserHandler) GetTraffic(c *fiber.Ctx) error {
	id := c.Params("id")
	ctx := context.Background()

	if !isUUID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid user id",
		})
	}

	var used int64
	var limit *int64
	err := h.db.QueryRow(ctx, `
		SELECT u.traffic_used, p.traffic_limit
		FROM users u
		LEFT JOIN plans p ON u.plan_id = p.id
		WHERE u.id = $1
	`, id).Scan(&used, &limit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "user not found",
			})
		}
		log.Printf("admin user traffic %s: %v", id, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch traffic",
		})
	}

	// A plan with no limit and a plan with a zero limit are both "no ceiling":
	// reporting either as 0 remaining would show an unlimited user as exhausted.
	resp := userTrafficResponse{TrafficUsed: used, TrafficLimit: limit}
	if limit == nil || *limit <= 0 {
		resp.Unlimited = true
	} else {
		remaining := *limit - used
		if remaining < 0 {
			remaining = 0
		}
		resp.TrafficRemaining = &remaining
		resp.Percentage = math.Min(float64(used)/float64(*limit)*100, 100)
	}

	window := services.ParseTrafficWindow(c.Query("window", string(services.WindowMonth)))
	resp.Window = string(window)

	byNode, err := h.stats.GetUserNodeTraffic(ctx, id, window)
	if err != nil {
		log.Printf("admin user node traffic %s: %v", id, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch per-node traffic",
		})
	}
	resp.ByNode = byNode

	return c.JSON(resp)
}

// GetLoginHistory returns a user's login attempts, newest first.
// @Summary Get user login history
// @Description Returns successful and failed login attempts for one user, newest first
// @Tags admin-users
// @Produce json
// @Param id path string true "User ID"
// @Param limit query int false "Maximum attempts to return (default 50, max 200)"
// @Success 200 {array} services.LoginEntry
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/users/{id}/login-history [get]
func (h *AdminUserHandler) GetLoginHistory(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid user id",
		})
	}

	entries, err := h.logins.ListForUser(context.Background(), id, c.QueryInt("limit", 50))
	if err != nil {
		log.Printf("admin user login history %s: %v", id, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch login history",
		})
	}

	return c.JSON(entries)
}
