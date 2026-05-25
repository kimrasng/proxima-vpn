package handlers

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// Admin2FAHandler handles 2FA setup, enable, and disable for admin accounts.
type Admin2FAHandler struct {
	DB *pgxpool.Pool
}

// NewAdmin2FAHandler creates a new Admin2FAHandler.
func NewAdmin2FAHandler(db *pgxpool.Pool) *Admin2FAHandler {
	return &Admin2FAHandler{DB: db}
}

type enable2FARequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

type disable2FARequest struct {
	Code string `json:"code"`
}

// Status returns whether 2FA is currently enabled for the authenticated admin.
// @Summary Get 2FA status
// @Description Returns whether 2FA is enabled for the admin account
// @Tags admin-auth
// @Produce json
// @Success 200 {object} map[string]bool
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/auth/2fa/status [get]
func (h *Admin2FAHandler) Status(c *fiber.Ctx) error {
	adminID := c.Locals("admin_id").(string)

	var totpEnabled bool
	err := h.DB.QueryRow(context.Background(),
		"SELECT totp_enabled FROM admins WHERE id = $1", adminID,
	).Scan(&totpEnabled)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to fetch admin")
	}

	return c.JSON(fiber.Map{"enabled": totpEnabled})
}

// Setup generates a TOTP secret and otpauth URL for the authenticated admin.
// The secret is NOT saved to DB until the admin verifies it via Enable.
// @Summary Setup 2FA
// @Description Generate a TOTP secret and otpauth URL for the admin to scan
// @Tags admin-auth
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/auth/2fa/setup [get]
func (h *Admin2FAHandler) Setup(c *fiber.Ctx) error {
	adminID := c.Locals("admin_id").(string)

	var email string
	err := h.DB.QueryRow(context.Background(),
		"SELECT email FROM admins WHERE id = $1", adminID,
	).Scan(&email)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to fetch admin")
	}

	secret, url, err := crypto.GenerateTOTPSecret(email)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to generate TOTP secret")
	}

	return c.JSON(fiber.Map{
		"secret": secret,
		"url":    url,
	})
}

// Enable validates the TOTP code against the provided secret and saves it to DB.
// @Summary Enable 2FA
// @Description Verify TOTP code and enable 2FA for the admin account
// @Tags admin-auth
// @Accept json
// @Produce json
// @Param body body enable2FARequest true "Secret and TOTP code"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/auth/2fa/enable [post]
func (h *Admin2FAHandler) Enable(c *fiber.Ctx) error {
	adminID := c.Locals("admin_id").(string)

	var req enable2FARequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	if req.Secret == "" || req.Code == "" {
		return fiber.NewError(fiber.StatusBadRequest, "secret and code are required")
	}

	if !crypto.ValidateTOTP(req.Secret, req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "invalid TOTP code")
	}

	_, err := h.DB.Exec(context.Background(),
		"UPDATE admins SET totp_secret = $1, totp_enabled = true WHERE id = $2",
		req.Secret, adminID,
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to enable 2FA")
	}

	return c.JSON(fiber.Map{
		"message": "2FA enabled successfully",
	})
}

// Disable validates the TOTP code against the current secret and removes 2FA.
// @Summary Disable 2FA
// @Description Verify TOTP code and disable 2FA for the admin account
// @Tags admin-auth
// @Accept json
// @Produce json
// @Param body body disable2FARequest true "TOTP code"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/auth/2fa/disable [post]
func (h *Admin2FAHandler) Disable(c *fiber.Ctx) error {
	adminID := c.Locals("admin_id").(string)

	var req disable2FARequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	if req.Code == "" {
		return fiber.NewError(fiber.StatusBadRequest, "code is required")
	}

	var totpSecret *string
	err := h.DB.QueryRow(context.Background(),
		"SELECT totp_secret FROM admins WHERE id = $1", adminID,
	).Scan(&totpSecret)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to fetch admin")
	}

	if totpSecret == nil {
		return fiber.NewError(fiber.StatusBadRequest, "2FA is not enabled")
	}

	if !crypto.ValidateTOTP(*totpSecret, req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "invalid TOTP code")
	}

	_, err = h.DB.Exec(context.Background(),
		"UPDATE admins SET totp_secret = NULL, totp_enabled = false WHERE id = $1",
		adminID,
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to disable 2FA")
	}

	return c.JSON(fiber.Map{
		"message": "2FA disabled successfully",
	})
}
