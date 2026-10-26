package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
)

// ListLocalizedAnnouncements leaves the shared user portal handler untouched.
func (h *UserPortalHandler) ListLocalizedAnnouncements(c *fiber.Ctx) error {
	language := c.Query("lang")
	if language != "" && language != "ko" && language != "en" && language != "zh" {
		return badRequest(c, "language must be ko, en or zh")
	}
	rows, err := h.db.Query(c.UserContext(), `
		SELECT a.id, COALESCE(t.title, a.title), COALESCE(t.content, a.content),
			COALESCE(t.rendered_html, a.rendered_html), a.format,
			a.image_url, a.is_active, a.expires_at, a.created_at
		FROM announcements a
		LEFT JOIN announcement_translations t ON t.announcement_id=a.id
			AND t.language=COALESCE(NULLIF($1, ''), (SELECT NULLIF(language, '') FROM users WHERE id=$2), 'en')
		WHERE a.is_active=true AND (a.expires_at IS NULL OR a.expires_at>NOW())
		ORDER BY a.created_at DESC`, language, c.Locals("user_id").(string))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to list announcements"})
	}
	defer rows.Close()
	type item struct {
		ID          string     `json:"id"`
		Title       string     `json:"title"`
		Content     string     `json:"content"`
		ContentHTML string     `json:"content_html"`
		Format      string     `json:"format"`
		ImageURL    *string    `json:"image_url"`
		IsActive    bool       `json:"is_active"`
		ExpiresAt   *time.Time `json:"expires_at"`
		CreatedAt   time.Time  `json:"created_at"`
	}
	items := make([]item, 0)
	for rows.Next() {
		var a item
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.ContentHTML, &a.Format,
			&a.ImageURL, &a.IsActive, &a.ExpiresAt, &a.CreatedAt); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "failed to read announcements"})
		}
		items = append(items, a)
	}
	if rows.Err() != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to list announcements"})
	}
	return c.JSON(items)
}
