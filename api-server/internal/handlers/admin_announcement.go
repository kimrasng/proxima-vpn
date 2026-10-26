package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type AdminAnnouncementHandler struct {
	db *pgxpool.Pool
}

func NewAdminAnnouncementHandler(db *pgxpool.Pool) *AdminAnnouncementHandler {
	return &AdminAnnouncementHandler{db: db}
}

type announcementItem struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Content      string            `json:"content"`
	Format       string            `json:"format"`
	RenderedHTML string            `json:"rendered_html"`
	Translations []translationItem `json:"translations"`
	ImageURL     *string           `json:"image_url"`
	IsActive     bool              `json:"is_active"`
	ExpiresAt    *time.Time        `json:"expires_at"`
	CreatedAt    time.Time         `json:"created_at"`
}

type translationItem struct {
	Language     string `json:"language"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	RenderedHTML string `json:"-"`
}

type createAnnouncementRequest struct {
	Title        string            `json:"title"`
	Content      string            `json:"content"`
	Format       string            `json:"format"`
	Translations []translationItem `json:"translations"`
	ImageURL     *string           `json:"image_url"`
	ExpiresAt    *string           `json:"expires_at"`
}

type updateAnnouncementRequest struct {
	Title        *string            `json:"title"`
	Content      *string            `json:"content"`
	Format       *string            `json:"format"`
	Translations *[]translationItem `json:"translations"`
	ImageURL     json.RawMessage    `json:"image_url"`
	IsActive     *bool              `json:"is_active"`
	ExpiresAt    *string            `json:"expires_at"`
}

const announcementColumns = `a.id, a.title, a.content, a.format, a.rendered_html,
	a.image_url, a.is_active, a.expires_at, a.created_at,
	COALESCE((SELECT jsonb_agg(jsonb_build_object('language', t.language, 'title', t.title, 'content', t.content) ORDER BY t.language)
		FROM announcement_translations t WHERE t.announcement_id = a.id), '[]'::jsonb)`

func scanAnnouncement(row pgx.Row, a *announcementItem) error {
	return row.Scan(&a.ID, &a.Title, &a.Content, &a.Format, &a.RenderedHTML,
		&a.ImageURL, &a.IsActive, &a.ExpiresAt, &a.CreatedAt, &a.Translations)
}

func renderAnnouncement(format, content string, translations []translationItem) (string, error) {
	if len(content) > maxGuideContent {
		return "", errors.New("content is too long")
	}
	html, err := services.RenderGuide(format, content)
	if err != nil {
		return "", err
	}
	seen := make(map[string]bool, len(translations))
	for i := range translations {
		t := &translations[i]
		if (t.Language != "ko" && t.Language != "en" && t.Language != "zh") || seen[t.Language] {
			return "", errors.New("translations must have unique supported languages")
		}
		seen[t.Language] = true
		if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Content) == "" {
			return "", errors.New("translation title and content are required")
		}
		if len(t.Content) > maxGuideContent {
			return "", errors.New("translation content is too long")
		}
		t.RenderedHTML, err = services.RenderGuide(format, t.Content)
		if err != nil {
			return "", err
		}
	}
	return html, nil
}

func saveAnnouncementTranslations(ctx context.Context, tx pgx.Tx, id string, translations []translationItem) error {
	languages := make([]string, 0, len(translations))
	for _, t := range translations {
		languages = append(languages, t.Language)
		_, err := tx.Exec(ctx, `INSERT INTO announcement_translations
			(announcement_id, language, title, content, rendered_html) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (announcement_id, language) DO UPDATE
			SET title=EXCLUDED.title, content=EXCLUDED.content, rendered_html=EXCLUDED.rendered_html`,
			id, t.Language, t.Title, t.Content, t.RenderedHTML)
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `DELETE FROM announcement_translations WHERE announcement_id=$1 AND NOT (language=ANY($2::text[]))`, id, languages)
	return err
}

func (h *AdminAnnouncementHandler) Preview(c *fiber.Ctx) error {
	var req struct {
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	html, err := renderAnnouncement(req.Format, req.Content, nil)
	if err != nil {
		return badRequest(c, err.Error())
	}
	return c.JSON(fiber.Map{"html": html})
}

// List returns every announcement, including inactive and expired ones.
// @Summary List announcements
// @Description Returns all announcements for admin management
// @Tags admin-announcements
// @Produce json
// @Success 200 {array} announcementItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/announcements [get]
func (h *AdminAnnouncementHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT `+announcementColumns+` FROM announcements a ORDER BY a.created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list announcements"})
	}
	defer rows.Close()

	items := make([]announcementItem, 0)
	for rows.Next() {
		var a announcementItem
		if err := scanAnnouncement(rows, &a); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "failed to read announcements"})
		}
		items = append(items, a)
	}
	if rows.Err() != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to list announcements"})
	}

	return c.JSON(items)
}

// Create adds a new announcement.
// @Summary Create announcement
// @Description Creates a new announcement shown in the user portal
// @Tags admin-announcements
// @Accept json
// @Produce json
// @Param body body createAnnouncementRequest true "Announcement to create"
// @Success 201 {object} announcementItem
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/announcements [post]
func (h *AdminAnnouncementHandler) Create(c *fiber.Ctx) error {
	var req createAnnouncementRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Content) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title and content are required"})
	}
	if req.Format == "" {
		req.Format = services.GuideFormatMarkdown
	}
	html, err := renderAnnouncement(req.Format, req.Content, req.Translations)
	if err != nil {
		return badRequest(c, err.Error())
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid expires_at format, use RFC3339"})
		}
		expiresAt = &t
	}

	var a announcementItem
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to create announcement"})
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx,
		`INSERT INTO announcements (title, content, format, rendered_html, image_url, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		req.Title, req.Content, req.Format, html, req.ImageURL, expiresAt).Scan(&a.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create announcement"})
	}
	if err := saveAnnouncementTranslations(ctx, tx, a.ID, req.Translations); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to save announcement translations"})
	}
	if err := scanAnnouncement(tx.QueryRow(ctx, `SELECT `+announcementColumns+` FROM announcements a WHERE a.id=$1`, a.ID), &a); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to read announcement"})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to create announcement"})
	}

	return c.Status(fiber.StatusCreated).JSON(a)
}

// Update partially updates an announcement.
// @Summary Update announcement
// @Description Partially updates an announcement's fields
// @Tags admin-announcements
// @Accept json
// @Produce json
// @Param id path string true "Announcement ID"
// @Param body body updateAnnouncementRequest true "Fields to update"
// @Success 200 {object} announcementItem
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/announcements/{id} [put]
func (h *AdminAnnouncementHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return badRequest(c, "invalid announcement id")
	}

	var req updateAnnouncementRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Title == nil && req.Content == nil && req.Format == nil && req.Translations == nil &&
		req.ImageURL == nil && req.IsActive == nil && req.ExpiresAt == nil {
		return badRequest(c, "no fields to update")
	}
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to update announcement"})
	}
	defer tx.Rollback(ctx)
	var current announcementItem
	err = scanAnnouncement(tx.QueryRow(ctx, `SELECT `+announcementColumns+` FROM announcements a WHERE a.id=$1 FOR UPDATE`, id), &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(404).JSON(fiber.Map{"error": "announcement not found"})
	}
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to read announcement"})
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" || req.Content != nil && strings.TrimSpace(*req.Content) == "" {
		return badRequest(c, "title and content are required")
	}
	if req.Content != nil {
		current.Content = *req.Content
	}
	if req.Format != nil {
		current.Format = *req.Format
	}
	if req.Translations != nil {
		current.Translations = *req.Translations
	}
	html, err := renderAnnouncement(current.Format, current.Content, current.Translations)
	if err != nil {
		return badRequest(c, err.Error())
	}

	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Title != nil {
		setClauses = append(setClauses, "title = $"+itoa(argIdx))
		args = append(args, *req.Title)
		argIdx++
	}
	if req.Content != nil {
		setClauses = append(setClauses, "content = $"+itoa(argIdx))
		args = append(args, *req.Content)
		argIdx++
	}
	if req.Format != nil {
		setClauses = append(setClauses, "format = $"+itoa(argIdx))
		args = append(args, *req.Format)
		argIdx++
	}
	setClauses = append(setClauses, "rendered_html = $"+itoa(argIdx))
	args = append(args, html)
	argIdx++
	if req.ImageURL != nil {
		var imageURL *string
		if err := json.Unmarshal(req.ImageURL, &imageURL); err != nil {
			return badRequest(c, "invalid image_url")
		}
		setClauses = append(setClauses, "image_url = $"+itoa(argIdx))
		args = append(args, imageURL)
		argIdx++
	}
	if req.IsActive != nil {
		setClauses = append(setClauses, "is_active = $"+itoa(argIdx))
		args = append(args, *req.IsActive)
		argIdx++
	}
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			setClauses = append(setClauses, "expires_at = $"+itoa(argIdx))
			args = append(args, nil)
			argIdx++
		} else {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid expires_at format"})
			}
			setClauses = append(setClauses, "expires_at = $"+itoa(argIdx))
			args = append(args, t)
			argIdx++
		}
	}

	args = append(args, id)
	query := "UPDATE announcements SET " + strings.Join(setClauses, ", ") +
		" WHERE id = $" + itoa(argIdx) +
		" RETURNING id"

	var a announcementItem
	err = tx.QueryRow(ctx, query, args...).Scan(&a.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update announcement"})
	}
	if err := saveAnnouncementTranslations(ctx, tx, id, current.Translations); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to save announcement translations"})
	}
	if err := scanAnnouncement(tx.QueryRow(ctx, `SELECT `+announcementColumns+` FROM announcements a WHERE a.id=$1`, id), &a); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to read announcement"})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to update announcement"})
	}

	return c.JSON(a)
}

// Delete removes an announcement.
// @Summary Delete announcement
// @Description Deletes an announcement by ID
// @Tags admin-announcements
// @Produce json
// @Param id path string true "Announcement ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/announcements/{id} [delete]
func (h *AdminAnnouncementHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	_, err := h.db.Exec(context.Background(), `DELETE FROM announcements WHERE id = $1`, id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete announcement"})
	}

	return c.SendStatus(fiber.StatusNoContent)
}
