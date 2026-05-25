package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// UserDeviceHandler handles user device management endpoints.
type UserDeviceHandler struct {
	db *pgxpool.Pool
}

// NewUserDeviceHandler creates a new UserDeviceHandler.
func NewUserDeviceHandler(db *pgxpool.Pool) *UserDeviceHandler {
	return &UserDeviceHandler{db: db}
}

type createDeviceRequest struct {
	Name string `json:"name"`
}

type deviceResponse struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	XrayUUID        string    `json:"xray_uuid"`
	SubscriptionURL string    `json:"subscription_url,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// Create handles POST /api/v1/user/devices.
// @Summary Create device
// @Description Registers a new device for the authenticated user
// @Tags user-portal
// @Accept json
// @Produce json
// @Param body body createDeviceRequest true "Device name"
// @Success 201 {object} deviceResponse
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/devices [post]
func (h *UserDeviceHandler) Create(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var req createDeviceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" {
		req.Name = "Device"
	}

	var userStatus string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT status FROM users WHERE id = $1`,
		userID,
	).Scan(&userStatus)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	if userStatus != "active" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "active plan required to add devices",
		})
	}

	var deviceCount, maxDevices int
	err = h.db.QueryRow(
		context.Background(),
		`SELECT COUNT(*) FROM devices WHERE user_id = $1`,
		userID,
	).Scan(&deviceCount)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	err = h.db.QueryRow(
		context.Background(),
		`SELECT p.max_devices FROM plans p JOIN users u ON u.plan_id = p.id WHERE u.id = $1`,
		userID,
	).Scan(&maxDevices)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	if deviceCount >= maxDevices {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "device limit reached",
		})
	}

	xrayUUID := crypto.NewUUID()

	var resp deviceResponse
	err = h.db.QueryRow(
		context.Background(),
		`INSERT INTO devices (user_id, name, xray_uuid)
		 VALUES ($1, $2, $3)
		 RETURNING id, name, xray_uuid, created_at`,
		userID, req.Name, xrayUUID,
	).Scan(&resp.ID, &resp.Name, &resp.XrayUUID, &resp.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(resp)
}

// List handles GET /api/v1/user/devices.
// @Summary List devices
// @Description Returns all devices for the authenticated user
// @Tags user-portal
// @Produce json
// @Success 200 {array} deviceResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/devices [get]
func (h *UserDeviceHandler) List(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	var subToken string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT sub_token FROM users WHERE id = $1`,
		userID,
	).Scan(&subToken)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, name, xray_uuid, created_at FROM devices WHERE user_id = $1 ORDER BY created_at`,
		userID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}
	defer rows.Close()

	var results []deviceResponse
	for rows.Next() {
		var d deviceResponse
		if err := rows.Scan(&d.ID, &d.Name, &d.XrayUUID, &d.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "internal server error",
			})
		}
		d.SubscriptionURL = fmt.Sprintf("/sub/%s/%s", subToken, d.ID)
		results = append(results, d)
	}

	if results == nil {
		results = []deviceResponse{}
	}

	return c.JSON(results)
}

// Delete handles DELETE /api/v1/user/devices/:id.
// @Summary Delete device
// @Description Removes a device belonging to the authenticated user
// @Tags user-portal
// @Param id path string true "Device ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/devices/{id} [delete]
func (h *UserDeviceHandler) Delete(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	deviceID := c.Params("id")

	tag, err := h.db.Exec(
		context.Background(),
		`DELETE FROM devices WHERE id = $1 AND user_id = $2`,
		deviceID, userID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal server error",
		})
	}

	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "device not found",
		})
	}

	return c.SendStatus(fiber.StatusNoContent)
}
