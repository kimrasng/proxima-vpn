package handlers

import (
	"context"
	"strings"
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
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	ImageURL  *string    `json:"image_url"`
	IsActive  bool       `json:"is_active"`
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type createAnnouncementRequest struct {
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	ImageURL  *string `json:"image_url"`
	ExpiresAt *string `json:"expires_at"`
}

type updateAnnouncementRequest struct {
	Title     *string `json:"title"`
	Content   *string `json:"content"`
	ImageURL  *string `json:"image_url"`
	IsActive  *bool   `json:"is_active"`
	ExpiresAt *string `json:"expires_at"`
}

func (h *AdminAnnouncementHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT id, title, content, image_url, is_active, expires_at, created_at
		 FROM announcements ORDER BY created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list announcements"})
	}
	defer rows.Close()

	items := make([]announcementItem, 0)
	for rows.Next() {
		var a announcementItem
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.ImageURL, &a.IsActive, &a.ExpiresAt, &a.CreatedAt); err != nil {
			continue
		}
		items = append(items, a)
	}

	return c.JSON(items)
}

func (h *AdminAnnouncementHandler) Create(c *fiber.Ctx) error {
	var req createAnnouncementRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Title == "" || req.Content == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title and content are required"})
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
	err := h.db.QueryRow(
		context.Background(),
		`INSERT INTO announcements (title, content, image_url, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, title, content, image_url, is_active, expires_at, created_at`,
		req.Title, req.Content, req.ImageURL, expiresAt,
	).Scan(&a.ID, &a.Title, &a.Content, &a.ImageURL, &a.IsActive, &a.ExpiresAt, &a.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create announcement"})
	}

	return c.Status(fiber.StatusCreated).JSON(a)
}

func (h *AdminAnnouncementHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updateAnnouncementRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
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
	if req.ImageURL != nil {
		setClauses = append(setClauses, "image_url = $"+itoa(argIdx))
		args = append(args, *req.ImageURL)
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

	if len(setClauses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no fields to update"})
	}

	args = append(args, id)
	query := "UPDATE announcements SET " + strings.Join(setClauses, ", ") +
		" WHERE id = $" + itoa(argIdx) +
		" RETURNING id, title, content, image_url, is_active, expires_at, created_at"

	var a announcementItem
	err := h.db.QueryRow(context.Background(), query, args...).
		Scan(&a.ID, &a.Title, &a.Content, &a.ImageURL, &a.IsActive, &a.ExpiresAt, &a.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update announcement"})
	}

	return c.JSON(a)
}

func (h *AdminAnnouncementHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	_, err := h.db.Exec(context.Background(), `DELETE FROM announcements WHERE id = $1`, id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete announcement"})
	}

	return c.SendStatus(fiber.StatusNoContent)
}
