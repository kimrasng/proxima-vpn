package handlers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type AdminSettingsHandler struct {
	db *pgxpool.Pool
}

func NewAdminSettingsHandler(db *pgxpool.Pool) *AdminSettingsHandler {
	return &AdminSettingsHandler{db: db}
}

type settingItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// List returns all settings.
// @Summary List settings
// @Description Returns all application settings
// @Tags admin-settings
// @Produce json
// @Success 200 {array} settingItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/settings [get]
func (h *AdminSettingsHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(context.Background(), `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list settings"})
	}
	defer rows.Close()

	items := make([]settingItem, 0)
	for rows.Next() {
		var s settingItem
		if err := rows.Scan(&s.Key, &s.Value); err != nil {
			continue
		}
		items = append(items, s)
	}

	return c.JSON(items)
}

// Update upserts settings key-value pairs.
// @Summary Update settings
// @Description Upserts one or more settings key-value pairs
// @Tags admin-settings
// @Accept json
// @Produce json
// @Param body body map[string]interface{} true "Key-value pairs to upsert"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/settings [put]
func (h *AdminSettingsHandler) Update(c *fiber.Ctx) error {
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx := context.Background()
	for key, raw := range body {
		_, err := h.db.Exec(ctx,
			`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = $2`,
			key, settingValueToString(raw),
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update settings"})
		}
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// settingValueToString converts a JSON-decoded settings value (the `value`
// column is TEXT, but the admin Settings form - web/src/pages/admin/
// Settings.tsx - submits real booleans for its toggle fields, e.g.
// self_registration/telegram_enabled) into the string form callers that read
// it back expect (see e.g. "value = 'false'" in UserAuthHandler.Register).
// Without this, saving the Settings page always failed: pgx cannot bind a
// Go bool to a TEXT column, and every real save includes at least one
// boolean field.
func settingValueToString(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

const telegramAPIBase = "https://api.telegram.org"

func (h *AdminSettingsHandler) savedSettings(ctx context.Context, keys ...string) map[string]string {
	out := map[string]string{}
	rows, err := h.db.Query(ctx, `SELECT key, value FROM settings WHERE key = ANY($1)`, keys)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}

// TestS3 handles POST /api/v1/admin/settings/test/s3. It tests the values in
// the request (the form as typed, unsaved), filling blank fields from the
// saved settings, so an admin can check credentials before saving them.
// @Summary Test the S3 backup connection
// @Tags admin-settings
// @Accept json
// @Produce json
// @Success 200 {object} services.ConnectionResult
// @Security BearerAuth
// @Router /admin/settings/test/s3 [post]
func (h *AdminSettingsHandler) TestS3(c *fiber.Ctx) error {
	var body struct {
		Endpoint  string `json:"s3_endpoint"`
		Bucket    string `json:"s3_bucket"`
		AccessKey string `json:"s3_access_key"`
		SecretKey string `json:"s3_secret_key"`
		Region    string `json:"s3_region"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	saved := h.savedSettings(c.Context(), "s3_endpoint", "s3_bucket", "s3_access_key", "s3_secret_key", "s3_region")
	cfg := services.MergeS3(config.S3Config{
		Endpoint: strings.TrimSpace(body.Endpoint), Bucket: strings.TrimSpace(body.Bucket),
		AccessKey: strings.TrimSpace(body.AccessKey), SecretKey: strings.TrimSpace(body.SecretKey), Region: strings.TrimSpace(body.Region),
	}, config.S3Config{
		Endpoint: saved["s3_endpoint"], Bucket: saved["s3_bucket"], AccessKey: saved["s3_access_key"],
		SecretKey: saved["s3_secret_key"], Region: saved["s3_region"],
	})
	return c.JSON(services.CheckS3Connection(c.Context(), cfg))
}

// TestTelegram handles POST /api/v1/admin/settings/test/telegram. It checks
// the bot token and sends one test message to the chat, using the request
// values and falling back to the saved ones for blank fields.
// @Summary Send a Telegram test alert
// @Tags admin-settings
// @Accept json
// @Produce json
// @Success 200 {object} services.ConnectionResult
// @Security BearerAuth
// @Router /admin/settings/test/telegram [post]
func (h *AdminSettingsHandler) TestTelegram(c *fiber.Ctx) error {
	var body struct {
		Token  string `json:"telegram_bot_token"`
		ChatID string `json:"telegram_chat_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	saved := h.savedSettings(c.Context(), "telegram_bot_token", "telegram_chat_id")
	token, chat := strings.TrimSpace(body.Token), strings.TrimSpace(body.ChatID)
	if token == "" {
		token = saved["telegram_bot_token"]
	}
	if chat == "" {
		chat = saved["telegram_chat_id"]
	}
	return c.JSON(services.CheckTelegramConnection(c.Context(), telegramAPIBase, token, chat))
}
