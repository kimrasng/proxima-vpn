package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminPlanRequestHandler handles admin plan request review endpoints.
type AdminPlanRequestHandler struct {
	db *pgxpool.Pool
}

// NewAdminPlanRequestHandler creates a new AdminPlanRequestHandler.
func NewAdminPlanRequestHandler(db *pgxpool.Pool) *AdminPlanRequestHandler {
	return &AdminPlanRequestHandler{db: db}
}

type adminPlanRequestItem struct {
	ID         string     `json:"id"`
	UserEmail  string     `json:"user_email"`
	UserName   string     `json:"user_name"`
	PlanName   string     `json:"plan_name"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
}

type reviewRequestBody struct {
	Action string `json:"action"`
}

// List handles GET /api/v1/admin/plan-requests.
// @Summary List plan requests
// @Description Returns all plan requests with optional status filter
// @Tags admin-plan-requests
// @Produce json
// @Param status query string false "Filter by status (pending, approved, rejected)"
// @Success 200 {array} adminPlanRequestItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plan-requests [get]
func (h *AdminPlanRequestHandler) List(c *fiber.Ctx) error {
	statusFilter := c.Query("status")

	query := `SELECT pr.id, u.email, u.name, p.name, pr.status, pr.created_at, pr.reviewed_at
		 FROM plan_requests pr
		 JOIN users u ON u.id = pr.user_id
		 JOIN plans p ON p.id = pr.plan_id`
	args := []interface{}{}

	if statusFilter != "" {
		query += ` WHERE pr.status = $1`
		args = append(args, statusFilter)
	}

	query += ` ORDER BY pr.created_at DESC`

	rows, err := h.db.Query(context.Background(), query, args...)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	defer rows.Close()

	var results []adminPlanRequestItem
	for rows.Next() {
		var r adminPlanRequestItem
		if err := rows.Scan(&r.ID, &r.UserEmail, &r.UserName, &r.PlanName, &r.Status, &r.CreatedAt, &r.ReviewedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
		results = append(results, r)
	}

	if results == nil {
		results = []adminPlanRequestItem{}
	}

	return c.JSON(results)
}

// Review handles PUT /api/v1/admin/plan-requests/:id.
// @Summary Review plan request
// @Description Approve or reject a pending plan request
// @Tags admin-plan-requests
// @Accept json
// @Produce json
// @Param id path string true "Plan Request ID"
// @Param body body reviewRequestBody true "Action (approve or reject)"
// @Success 200 {object} adminPlanRequestItem
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plan-requests/{id} [put]
func (h *AdminPlanRequestHandler) Review(c *fiber.Ctx) error {
	requestID := c.Params("id")
	adminID := c.Locals("admin_id").(string)

	var req reviewRequestBody
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Action != "approve" && req.Action != "reject" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "action must be 'approve' or 'reject'",
		})
	}

	var userID, planID, currentStatus string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT user_id, plan_id, status FROM plan_requests WHERE id = $1`,
		requestID,
	).Scan(&userID, &planID, &currentStatus)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "plan request not found",
		})
	}

	if currentStatus != "pending" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "request already reviewed",
		})
	}

	if req.Action == "approve" {
		var durationDays int
		err = h.db.QueryRow(
			context.Background(),
			`SELECT duration_days FROM plans WHERE id = $1`,
			planID,
		).Scan(&durationDays)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}

		_, err = h.db.Exec(
			context.Background(),
			`UPDATE plan_requests SET status = 'approved', reviewed_by = $1, reviewed_at = NOW() WHERE id = $2`,
			adminID, requestID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}

		_, err = h.db.Exec(
			context.Background(),
			`UPDATE users SET plan_id = $1, plan_started_at = NOW(), plan_expires_at = NOW() + make_interval(days => $2),
			 traffic_used = 0, status = 'active', is_active = true
			 WHERE id = $3`,
			planID, durationDays, userID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
	} else {
		_, err = h.db.Exec(
			context.Background(),
			`UPDATE plan_requests SET status = 'rejected', reviewed_by = $1, reviewed_at = NOW() WHERE id = $2`,
			adminID, requestID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
	}

	var result adminPlanRequestItem
	err = h.db.QueryRow(
		context.Background(),
		`SELECT pr.id, u.email, u.name, p.name, pr.status, pr.created_at, pr.reviewed_at
		 FROM plan_requests pr
		 JOIN users u ON u.id = pr.user_id
		 JOIN plans p ON p.id = pr.plan_id
		 WHERE pr.id = $1`,
		requestID,
	).Scan(&result.ID, &result.UserEmail, &result.UserName, &result.PlanName, &result.Status, &result.CreatedAt, &result.ReviewedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	return c.JSON(result)
}
