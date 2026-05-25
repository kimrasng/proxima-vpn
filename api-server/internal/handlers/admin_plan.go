package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminPlanHandler handles admin plan CRUD endpoints.
type AdminPlanHandler struct {
	db *pgxpool.Pool
}

// NewAdminPlanHandler creates a new AdminPlanHandler.
func NewAdminPlanHandler(db *pgxpool.Pool) *AdminPlanHandler {
	return &AdminPlanHandler{db: db}
}

type createPlanRequest struct {
	Name         string  `json:"name"`
	TrafficLimit *int64  `json:"traffic_limit"`
	DurationDays int     `json:"duration_days"`
	MaxDevices   int     `json:"max_devices"`
	SpeedLimit   *int    `json:"speed_limit"`
	NodeGroupID  string  `json:"node_group_id"`
}

type updatePlanRequest struct {
	Name         *string `json:"name"`
	TrafficLimit *int64  `json:"traffic_limit"`
	DurationDays *int    `json:"duration_days"`
	MaxDevices   *int    `json:"max_devices"`
	SpeedLimit   *int    `json:"speed_limit"`
	NodeGroupID  *string `json:"node_group_id"`
	IsActive     *bool   `json:"is_active"`
}

type planResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	TrafficLimit  *int64    `json:"traffic_limit"`
	DurationDays  int       `json:"duration_days"`
	MaxDevices    int       `json:"max_devices"`
	SpeedLimit    *int      `json:"speed_limit"`
	NodeGroupID   string    `json:"node_group_id"`
	NodeGroupName *string   `json:"node_group_name"`
	IsActive      bool      `json:"is_active"`
	CreatedAt     time.Time `json:"created_at"`
}

// Create creates a new plan.
// @Summary Create plan
// @Description Creates a new subscription plan
// @Tags admin-plans
// @Accept json
// @Produce json
// @Param body body createPlanRequest true "Plan details"
// @Success 201 {object} planResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans [post]
func (h *AdminPlanHandler) Create(c *fiber.Ctx) error {
	var req createPlanRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name is required",
		})
	}
	if req.DurationDays <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "duration_days must be positive",
		})
	}
	if req.MaxDevices <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "max_devices must be positive",
		})
	}
	if req.NodeGroupID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "node_group_id is required",
		})
	}

	var plan planResponse
	err := h.db.QueryRow(
		context.Background(),
		`INSERT INTO plans (name, traffic_limit, duration_days, max_devices, speed_limit, node_group_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, name, traffic_limit, duration_days, max_devices, speed_limit, node_group_id, is_active, created_at`,
		req.Name, req.TrafficLimit, req.DurationDays, req.MaxDevices, req.SpeedLimit, req.NodeGroupID,
	).Scan(&plan.ID, &plan.Name, &plan.TrafficLimit, &plan.DurationDays, &plan.MaxDevices, &plan.SpeedLimit, &plan.NodeGroupID, &plan.IsActive, &plan.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create plan",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(plan)
}

// List returns all plans with node group info.
// @Summary List plans
// @Description Returns all plans with node group information
// @Tags admin-plans
// @Produce json
// @Success 200 {array} planResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans [get]
func (h *AdminPlanHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT p.id, p.name, p.traffic_limit, p.duration_days, p.max_devices, p.speed_limit,
		        p.node_group_id, ng.name AS node_group_name, p.is_active, p.created_at
		 FROM plans p
		 LEFT JOIN node_groups ng ON p.node_group_id = ng.id
		 ORDER BY p.created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list plans",
		})
	}
	defer rows.Close()

	plans := make([]planResponse, 0)
	for rows.Next() {
		var p planResponse
		if err := rows.Scan(&p.ID, &p.Name, &p.TrafficLimit, &p.DurationDays, &p.MaxDevices, &p.SpeedLimit, &p.NodeGroupID, &p.NodeGroupName, &p.IsActive, &p.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan plan",
			})
		}
		plans = append(plans, p)
	}

	return c.JSON(plans)
}

// Get returns a single plan by ID with node group info.
// @Summary Get plan
// @Description Returns a single plan by ID with node group info
// @Tags admin-plans
// @Produce json
// @Param id path string true "Plan ID"
// @Success 200 {object} planResponse
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans/{id} [get]
func (h *AdminPlanHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")

	var plan planResponse
	err := h.db.QueryRow(
		context.Background(),
		`SELECT p.id, p.name, p.traffic_limit, p.duration_days, p.max_devices, p.speed_limit,
		        p.node_group_id, ng.name AS node_group_name, p.is_active, p.created_at
		 FROM plans p
		 LEFT JOIN node_groups ng ON p.node_group_id = ng.id
		 WHERE p.id = $1`,
		id,
	).Scan(&plan.ID, &plan.Name, &plan.TrafficLimit, &plan.DurationDays, &plan.MaxDevices, &plan.SpeedLimit, &plan.NodeGroupID, &plan.NodeGroupName, &plan.IsActive, &plan.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "plan not found",
		})
	}

	return c.JSON(plan)
}

// Update updates an existing plan (partial update).
// @Summary Update plan
// @Description Partially updates an existing plan
// @Tags admin-plans
// @Accept json
// @Produce json
// @Param id path string true "Plan ID"
// @Param body body updatePlanRequest true "Fields to update"
// @Success 200 {object} planResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans/{id} [put]
func (h *AdminPlanHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updatePlanRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Build dynamic update query
	setClauses := ""
	args := []interface{}{}
	argIdx := 1

	if req.Name != nil {
		setClauses += comma(setClauses) + "name = $" + itoa(argIdx)
		args = append(args, *req.Name)
		argIdx++
	}
	if req.TrafficLimit != nil {
		setClauses += comma(setClauses) + "traffic_limit = $" + itoa(argIdx)
		args = append(args, *req.TrafficLimit)
		argIdx++
	}
	if req.DurationDays != nil {
		setClauses += comma(setClauses) + "duration_days = $" + itoa(argIdx)
		args = append(args, *req.DurationDays)
		argIdx++
	}
	if req.MaxDevices != nil {
		setClauses += comma(setClauses) + "max_devices = $" + itoa(argIdx)
		args = append(args, *req.MaxDevices)
		argIdx++
	}
	if req.SpeedLimit != nil {
		setClauses += comma(setClauses) + "speed_limit = $" + itoa(argIdx)
		args = append(args, *req.SpeedLimit)
		argIdx++
	}
	if req.NodeGroupID != nil {
		setClauses += comma(setClauses) + "node_group_id = $" + itoa(argIdx)
		args = append(args, *req.NodeGroupID)
		argIdx++
	}
	if req.IsActive != nil {
		setClauses += comma(setClauses) + "is_active = $" + itoa(argIdx)
		args = append(args, *req.IsActive)
		argIdx++
	}

	if setClauses == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no fields to update",
		})
	}

	args = append(args, id)
	query := "UPDATE plans SET " + setClauses + " WHERE id = $" + itoa(argIdx)

	result, err := h.db.Exec(context.Background(), query, args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update plan",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "plan not found",
		})
	}

	return h.Get(c)
}

// Delete soft-deletes a plan by setting is_active to false.
// @Summary Delete plan
// @Description Soft-deletes a plan by deactivating it
// @Tags admin-plans
// @Produce json
// @Param id path string true "Plan ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans/{id} [delete]
func (h *AdminPlanHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	result, err := h.db.Exec(
		context.Background(),
		`UPDATE plans SET is_active = false WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete plan",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "plan not found",
		})
	}

	return c.JSON(fiber.Map{"message": "plan deactivated"})
}

// comma returns ", " if s is non-empty, otherwise "".
func comma(s string) string {
	if s == "" {
		return ""
	}
	return ", "
}

// itoa converts an int to its string representation.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
