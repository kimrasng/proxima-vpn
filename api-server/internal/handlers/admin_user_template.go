package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminUserTemplateHandler handles admin user template CRUD endpoints.
type AdminUserTemplateHandler struct {
	db *pgxpool.Pool
}

// NewAdminUserTemplateHandler creates a new AdminUserTemplateHandler.
func NewAdminUserTemplateHandler(db *pgxpool.Pool) *AdminUserTemplateHandler {
	return &AdminUserTemplateHandler{db: db}
}

type createUserTemplateRequest struct {
	Name         string  `json:"name"`
	TrafficLimit *int64  `json:"traffic_limit"`
	DurationDays int     `json:"duration_days"`
	MaxDevices   int     `json:"max_devices"`
	SpeedLimit   *int    `json:"speed_limit"`
	NodeGroupID  *string `json:"node_group_id"`
}

type updateUserTemplateRequest struct {
	Name         *string `json:"name"`
	TrafficLimit *int64  `json:"traffic_limit"`
	DurationDays *int    `json:"duration_days"`
	MaxDevices   *int    `json:"max_devices"`
	SpeedLimit   *int    `json:"speed_limit"`
	NodeGroupID  *string `json:"node_group_id"`
}

type userTemplateResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	TrafficLimit  *int64    `json:"traffic_limit"`
	DurationDays  int       `json:"duration_days"`
	MaxDevices    int       `json:"max_devices"`
	SpeedLimit    *int      `json:"speed_limit"`
	NodeGroupID   *string   `json:"node_group_id"`
	NodeGroupName *string   `json:"node_group_name"`
	CreatedAt     time.Time `json:"created_at"`
}

// Create creates a new user template.
// @Summary Create user template
// @Description Creates a new user template for quick user provisioning
// @Tags admin-user-templates
// @Accept json
// @Produce json
// @Param body body createUserTemplateRequest true "Template details"
// @Success 201 {object} userTemplateResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/user-templates [post]
func (h *AdminUserTemplateHandler) Create(c *fiber.Ctx) error {
	var req createUserTemplateRequest
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

	var tmpl userTemplateResponse
	err := h.db.QueryRow(
		context.Background(),
		`INSERT INTO user_templates (name, traffic_limit, duration_days, max_devices, speed_limit, node_group_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, name, traffic_limit, duration_days, max_devices, speed_limit, node_group_id, created_at`,
		req.Name, req.TrafficLimit, req.DurationDays, req.MaxDevices, req.SpeedLimit, req.NodeGroupID,
	).Scan(&tmpl.ID, &tmpl.Name, &tmpl.TrafficLimit, &tmpl.DurationDays, &tmpl.MaxDevices, &tmpl.SpeedLimit, &tmpl.NodeGroupID, &tmpl.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create user template",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(tmpl)
}

// List returns all user templates.
// @Summary List user templates
// @Description Returns all user templates with node group info
// @Tags admin-user-templates
// @Produce json
// @Success 200 {array} userTemplateResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/user-templates [get]
func (h *AdminUserTemplateHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT ut.id, ut.name, ut.traffic_limit, ut.duration_days, ut.max_devices, ut.speed_limit,
		        ut.node_group_id, ng.name AS node_group_name, ut.created_at
		 FROM user_templates ut
		 LEFT JOIN node_groups ng ON ut.node_group_id = ng.id
		 ORDER BY ut.created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list user templates",
		})
	}
	defer rows.Close()

	templates := make([]userTemplateResponse, 0)
	for rows.Next() {
		var t userTemplateResponse
		if err := rows.Scan(&t.ID, &t.Name, &t.TrafficLimit, &t.DurationDays, &t.MaxDevices, &t.SpeedLimit, &t.NodeGroupID, &t.NodeGroupName, &t.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan user template",
			})
		}
		templates = append(templates, t)
	}

	return c.JSON(templates)
}

// Update updates an existing user template (partial update).
// @Summary Update user template
// @Description Partially updates an existing user template
// @Tags admin-user-templates
// @Accept json
// @Produce json
// @Param id path string true "Template ID"
// @Param body body updateUserTemplateRequest true "Fields to update"
// @Success 200 {object} userTemplateResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/user-templates/{id} [put]
func (h *AdminUserTemplateHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updateUserTemplateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

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

	if setClauses == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no fields to update",
		})
	}

	args = append(args, id)
	query := "UPDATE user_templates SET " + setClauses + " WHERE id = $" + itoa(argIdx)

	result, err := h.db.Exec(context.Background(), query, args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update user template",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user template not found",
		})
	}

	// Return updated template
	var tmpl userTemplateResponse
	err = h.db.QueryRow(
		context.Background(),
		`SELECT ut.id, ut.name, ut.traffic_limit, ut.duration_days, ut.max_devices, ut.speed_limit,
		        ut.node_group_id, ng.name AS node_group_name, ut.created_at
		 FROM user_templates ut
		 LEFT JOIN node_groups ng ON ut.node_group_id = ng.id
		 WHERE ut.id = $1`,
		id,
	).Scan(&tmpl.ID, &tmpl.Name, &tmpl.TrafficLimit, &tmpl.DurationDays, &tmpl.MaxDevices, &tmpl.SpeedLimit, &tmpl.NodeGroupID, &tmpl.NodeGroupName, &tmpl.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user template not found",
		})
	}

	return c.JSON(tmpl)
}

// Delete deletes a user template.
// @Summary Delete user template
// @Description Deletes a user template by ID
// @Tags admin-user-templates
// @Param id path string true "Template ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/user-templates/{id} [delete]
func (h *AdminUserTemplateHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	result, err := h.db.Exec(
		context.Background(),
		`DELETE FROM user_templates WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete user template",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user template not found",
		})
	}

	return c.JSON(fiber.Map{"message": "user template deleted"})
}
