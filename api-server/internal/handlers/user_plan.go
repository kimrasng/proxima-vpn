package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserPlanHandler handles user plan request endpoints.
type UserPlanHandler struct {
	db *pgxpool.Pool
}

// NewUserPlanHandler creates a new UserPlanHandler.
func NewUserPlanHandler(db *pgxpool.Pool) *UserPlanHandler {
	return &UserPlanHandler{db: db}
}

type createPlanRequestBody struct {
	PlanID string `json:"plan_id"`
}

type planRequestResponse struct {
	ID         string     `json:"id"`
	PlanID     string     `json:"plan_id"`
	PlanName   string     `json:"plan_name"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
}

// CreateRequest handles POST /api/v1/user/plan-requests.
// @Summary Create plan request
// @Description Submit a request for a subscription plan
// @Tags user-plans
// @Accept json
// @Produce json
// @Param body body createPlanRequestBody true "Plan ID to request"
// @Success 201 {object} planRequestResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/plan-requests [post]
func (h *UserPlanHandler) CreateRequest(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req createPlanRequestBody
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.PlanID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plan_id is required",
		})
	}

	var planActive bool
	err := h.db.QueryRow(
		context.Background(),
		`SELECT is_active FROM plans WHERE id = $1`,
		req.PlanID,
	).Scan(&planActive)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plan not found",
		})
	}
	if !planActive {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "plan is not active",
		})
	}

	var hasPending bool
	err = h.db.QueryRow(
		context.Background(),
		`SELECT EXISTS(SELECT 1 FROM plan_requests WHERE user_id = $1 AND status = 'pending')`,
		userID,
	).Scan(&hasPending)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	if hasPending {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "you already have a pending plan request",
		})
	}

	var resp planRequestResponse
	err = h.db.QueryRow(
		context.Background(),
		`INSERT INTO plan_requests (user_id, plan_id, status)
		 VALUES ($1, $2, 'pending')
		 RETURNING id, plan_id, status, created_at`,
		userID, req.PlanID,
	).Scan(&resp.ID, &resp.PlanID, &resp.Status, &resp.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	_ = h.db.QueryRow(
		context.Background(),
		`SELECT name FROM plans WHERE id = $1`,
		resp.PlanID,
	).Scan(&resp.PlanName)

	return c.Status(fiber.StatusCreated).JSON(resp)
}

// ListRequests handles GET /api/v1/user/plan-requests.
// @Summary List my plan requests
// @Description Returns the authenticated user's plan requests
// @Tags user-plans
// @Produce json
// @Success 200 {array} planRequestResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/plan-requests [get]
func (h *UserPlanHandler) ListRequests(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	rows, err := h.db.Query(
		context.Background(),
		`SELECT pr.id, pr.plan_id, p.name, pr.status, pr.created_at, pr.reviewed_at
		 FROM plan_requests pr
		 JOIN plans p ON p.id = pr.plan_id
		 WHERE pr.user_id = $1
		 ORDER BY pr.created_at DESC`,
		userID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	defer rows.Close()

	var results []planRequestResponse
	for rows.Next() {
		var r planRequestResponse
		if err := rows.Scan(&r.ID, &r.PlanID, &r.PlanName, &r.Status, &r.CreatedAt, &r.ReviewedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
		results = append(results, r)
	}

	if results == nil {
		results = []planRequestResponse{}
	}

	return c.JSON(results)
}

type userPlanItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TrafficLimit *int64 `json:"traffic_limit"`
	DurationDays int    `json:"duration_days"`
	MaxDevices   int    `json:"max_devices"`
	SpeedLimit   *int   `json:"speed_limit"`
}

// ListPlans returns all active plans available for users.
// @Summary List available plans
// @Description Returns all active subscription plans
// @Tags user-plans
// @Produce json
// @Success 200 {array} userPlanItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/plans [get]
func (h *UserPlanHandler) ListPlans(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, name, traffic_limit, duration_days, max_devices, speed_limit
		 FROM plans WHERE is_active = true ORDER BY name`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
	}
	defer rows.Close()

	items := make([]userPlanItem, 0)
	for rows.Next() {
		var p userPlanItem
		if err := rows.Scan(&p.ID, &p.Name, &p.TrafficLimit, &p.DurationDays, &p.MaxDevices, &p.SpeedLimit); err != nil {
			continue
		}
		items = append(items, p)
	}

	return c.JSON(items)
}
