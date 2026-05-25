package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminAnnouncementHandler struct {
	db *pgxpool.Pool
}

func NewAdminAnnouncementHandler(db *pgxpool.Pool) *AdminAnnouncementHandler {
	return &AdminAnnouncementHandler{db: db}
}

type announcementItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

type createAnnouncementRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type updateAnnouncementRequest struct {
	Title    *string `json:"title"`
	Content  *string `json:"content"`
	IsActive *bool   `json:"is_active"`
}

// List returns all announcements.
// @Summary List announcements
// @Description Returns all announcements ordered by creation date
// @Tags admin-announcements
// @Produce json
// @Success 200 {array} announcementItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/announcements [get]
func (h *AdminAnnouncementHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, title, content, is_active, created_at FROM announcements ORDER BY created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list announcements"})
	}
	defer rows.Close()

	items := make([]announcementItem, 0)
	for rows.Next() {
		var a announcementItem
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.IsActive, &a.CreatedAt); err != nil {
			continue
		}
		items = append(items, a)
	}

	return c.JSON(items)
}

// Create creates a new announcement.
// @Summary Create announcement
// @Description Creates a new announcement
// @Tags admin-announcements
// @Accept json
// @Produce json
// @Param body body createAnnouncementRequest true "Announcement details"
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
	if req.Title == "" || req.Content == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title and content are required"})
	}

	var a announcementItem
	err := h.db.QueryRow(
		context.Background(),
		`INSERT INTO announcements (title, content) VALUES ($1, $2) RETURNING id, title, content, is_active, created_at`,
		req.Title, req.Content,
	).Scan(&a.ID, &a.Title, &a.Content, &a.IsActive, &a.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create announcement"})
	}

	return c.Status(fiber.StatusCreated).JSON(a)
}

// Update updates an announcement.
// @Summary Update announcement
// @Description Partially updates an announcement
// @Tags admin-announcements
// @Accept json
// @Produce json
// @Param id path string true "Announcement ID"
// @Param body body updateAnnouncementRequest true "Fields to update"
// @Success 200 {object} announcementItem
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/announcements/{id} [put]
func (h *AdminAnnouncementHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updateAnnouncementRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	setClauses := ""
	args := []interface{}{}
	argIdx := 1

	if req.Title != nil {
		setClauses += "title = $" + itoa(argIdx) + ", "
		args = append(args, *req.Title)
		argIdx++
	}
	if req.Content != nil {
		setClauses += "content = $" + itoa(argIdx) + ", "
		args = append(args, *req.Content)
		argIdx++
	}
	if req.IsActive != nil {
		setClauses += "is_active = $" + itoa(argIdx) + ", "
		args = append(args, *req.IsActive)
		argIdx++
	}

	if setClauses == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no fields to update"})
	}

	setClauses = setClauses[:len(setClauses)-2]
	args = append(args, id)

	var a announcementItem
	err := h.db.QueryRow(
		context.Background(),
		"UPDATE announcements SET "+setClauses+" WHERE id = $"+itoa(argIdx)+" RETURNING id, title, content, is_active, created_at",
		args...,
	).Scan(&a.ID, &a.Title, &a.Content, &a.IsActive, &a.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update announcement"})
	}

	return c.JSON(a)
}

// Delete deletes an announcement.
// @Summary Delete announcement
// @Description Deletes an announcement by ID
// @Tags admin-announcements
// @Param id path string true "Announcement ID"
// @Success 204
// @Failure 500 {object} map[string]string
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
