package handlers

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
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
