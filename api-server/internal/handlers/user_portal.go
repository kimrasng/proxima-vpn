package handlers

import (
	"context"
	"math"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// UserPortalHandler handles user self-service endpoints.
type UserPortalHandler struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

// NewUserPortalHandler creates a new UserPortalHandler.
func NewUserPortalHandler(db *pgxpool.Pool, rdb *redis.Client) *UserPortalHandler {
	return &UserPortalHandler{db: db, redis: rdb}
}

// GetProfile returns the authenticated user's profile and plan info.
// @Summary Get profile
// @Description Returns the authenticated user's profile and plan info
// @Tags user-portal
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/profile [get]
func (h *UserPortalHandler) GetProfile(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var (
		email          string
		name           string
		status         string
		trafficUsed    int64
		planName       *string
		trafficLimit   *int64
		planExpiresAt  *time.Time
		planStartedAt  *time.Time
	)

	err := h.db.QueryRow(context.Background(), `
		SELECT u.email, u.name, u.status, u.traffic_used,
		       p.name, p.traffic_limit, u.plan_expires_at, u.plan_started_at
		FROM users u
		LEFT JOIN plans p ON u.plan_id = p.id
		WHERE u.id = $1
	`, userID).Scan(&email, &name, &status, &trafficUsed,
		&planName, &trafficLimit, &planExpiresAt, &planStartedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch profile",
		})
	}

	return c.JSON(fiber.Map{
		"email":           email,
		"name":            name,
		"status":          status,
		"traffic_used":    trafficUsed,
		"plan_name":       planName,
		"traffic_limit":   trafficLimit,
		"plan_expires_at": planExpiresAt,
		"plan_started_at": planStartedAt,
	})
}

type updateProfileRequest struct {
	Email    *string `json:"email"`
	Name     *string `json:"name"`
	Password *string `json:"password"`
}

// UpdateProfile updates the authenticated user's profile fields.
// @Summary Update profile
// @Description Updates the authenticated user's email, name, or password
// @Tags user-portal
// @Accept json
// @Produce json
// @Param body body updateProfileRequest true "Fields to update"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/profile [put]
func (h *UserPortalHandler) UpdateProfile(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req updateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Email == nil && req.Name == nil && req.Password == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "at least one field is required",
		})
	}

	ctx := context.Background()

	if req.Email != nil {
		var exists bool
		err := h.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND id != $2)`,
			*req.Email, userID,
		).Scan(&exists)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
		if exists {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "email already in use",
			})
		}
		_, err = h.db.Exec(ctx,
			`UPDATE users SET email = $1, updated_at = NOW() WHERE id = $2`,
			*req.Email, userID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update email",
			})
		}
	}

	if req.Name != nil {
		_, err := h.db.Exec(ctx,
			`UPDATE users SET name = $1, updated_at = NOW() WHERE id = $2`,
			*req.Name, userID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update name",
			})
		}
	}

	if req.Password != nil {
		hash, err := crypto.HashPassword(*req.Password)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
		_, err = h.db.Exec(ctx,
			`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
			hash, userID,
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update password",
			})
		}
	}

	return c.JSON(fiber.Map{"message": "profile updated"})
}

// GetTrafficStats returns the user's traffic usage summary.
// @Summary Get traffic stats
// @Description Returns the user's traffic usage, limits, and remaining days
// @Tags user-portal
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/traffic [get]
func (h *UserPortalHandler) GetTrafficStats(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var (
		trafficUsed   int64
		trafficLimit  *int64
		planExpiresAt *time.Time
	)

	err := h.db.QueryRow(context.Background(), `
		SELECT u.traffic_used, p.traffic_limit, u.plan_expires_at
		FROM users u
		LEFT JOIN plans p ON u.plan_id = p.id
		WHERE u.id = $1
	`, userID).Scan(&trafficUsed, &trafficLimit, &planExpiresAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch traffic stats",
		})
	}

	var percentage float64
	if trafficLimit != nil && *trafficLimit > 0 {
		percentage = math.Min(float64(trafficUsed)/float64(*trafficLimit)*100, 100)
	}

	var daysRemaining int
	if planExpiresAt != nil {
		days := int(time.Until(*planExpiresAt).Hours() / 24)
		if days > 0 {
			daysRemaining = days
		}
	}

	return c.JSON(fiber.Map{
		"traffic_used":   trafficUsed,
		"traffic_limit":  trafficLimit,
		"percentage":     percentage,
		"plan_expires_at": planExpiresAt,
		"days_remaining": daysRemaining,
	})
}

// RegenerateSubToken generates a new subscription token, invalidating existing URLs.
// @Summary Regenerate subscription token
// @Description Generates a new subscription token, invalidating existing subscription URLs
// @Tags user-portal
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/sub-token/regenerate [post]
func (h *UserPortalHandler) RegenerateSubToken(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	newToken := crypto.NewUUID()

	_, err := h.db.Exec(context.Background(),
		`UPDATE users SET sub_token = $1, updated_at = NOW() WHERE id = $2`,
		newToken, userID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to regenerate subscription token",
		})
	}

	return c.JSON(fiber.Map{"sub_token": newToken})
}

// ListAnnouncements returns active announcements for users.
// @Summary List announcements
// @Description Returns all active announcements for users
// @Tags user-portal
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/announcements [get]
func (h *UserPortalHandler) ListAnnouncements(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, title, content, is_active, created_at FROM announcements WHERE is_active = true ORDER BY created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list announcements"})
	}
	defer rows.Close()

	type item struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Content   string    `json:"content"`
		IsActive  bool      `json:"is_active"`
		CreatedAt time.Time `json:"created_at"`
	}

	items := make([]item, 0)
	for rows.Next() {
		var a item
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.IsActive, &a.CreatedAt); err != nil {
			continue
		}
		items = append(items, a)
	}

	return c.JSON(items)
}
