package handlers

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

const (
	maxGuideTitle   = 200
	maxGuideSummary = 300
	maxGuideContent = 200 * 1024
)

type GuideHandler struct {
	db *pgxpool.Pool
}

func NewGuideHandler(db *pgxpool.Pool) *GuideHandler {
	return &GuideHandler{db: db}
}

type guideCategory struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

type adminGuide struct {
	ID           string    `json:"id"`
	CategoryID   *string   `json:"category_id"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Format       string    `json:"format"`
	Content      string    `json:"content"`
	RenderedHTML string    `json:"rendered_html"`
	IsPublished  bool      `json:"is_published"`
	SortOrder    int       `json:"sort_order"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type guideRequest struct {
	CategoryID  *string `json:"category_id"`
	Title       string  `json:"title"`
	Summary     string  `json:"summary"`
	Format      string  `json:"format"`
	Content     string  `json:"content"`
	IsPublished bool    `json:"is_published"`
	SortOrder   int     `json:"sort_order"`
}

type categoryRequest struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

const adminGuideColumns = `id, category_id, title, summary, format, content, rendered_html, is_published, sort_order, created_at, updated_at`

func scanAdminGuide(row pgx.Row) (adminGuide, error) {
	var g adminGuide
	err := row.Scan(&g.ID, &g.CategoryID, &g.Title, &g.Summary, &g.Format, &g.Content, &g.RenderedHTML, &g.IsPublished, &g.SortOrder, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func badRequest(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
}

func internalError(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
}

// validateGuide normalises the request and returns the sanitized HTML that
// will be stored next to the source.
func validateGuide(req *guideRequest) (string, string) {
	req.Title = strings.TrimSpace(req.Title)
	req.Summary = strings.TrimSpace(req.Summary)
	if req.CategoryID != nil && strings.TrimSpace(*req.CategoryID) == "" {
		req.CategoryID = nil
	}
	switch {
	case req.Title == "":
		return "", "title is required"
	case len([]rune(req.Title)) > maxGuideTitle:
		return "", "title is too long"
	case len([]rune(req.Summary)) > maxGuideSummary:
		return "", "summary is too long"
	case len(req.Content) > maxGuideContent:
		return "", "content is too long"
	case req.CategoryID != nil && !isUUID(*req.CategoryID):
		return "", "invalid category_id"
	}
	html, err := services.RenderGuide(req.Format, req.Content)
	if err != nil {
		return "", "format must be markdown or html"
	}
	return html, ""
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ListCategories handles GET /api/v1/admin/guides/categories.
// @Summary List guide categories
// @Tags admin-guides
// @Produce json
// @Success 200 {array} guideCategory
// @Security BearerAuth
// @Router /admin/guides/categories [get]
func (h *GuideHandler) ListCategories(c *fiber.Ctx) error {
	cats, err := h.categories(c)
	if err != nil {
		return internalError(c)
	}
	return c.JSON(cats)
}

func (h *GuideHandler) categories(c *fiber.Ctx) ([]guideCategory, error) {
	rows, err := h.db.Query(c.Context(), `SELECT id, name, sort_order FROM guide_categories ORDER BY sort_order, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]guideCategory, 0)
	for rows.Next() {
		var cat guideCategory
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, cat)
	}
	return out, rows.Err()
}

// CreateCategory handles POST /api/v1/admin/guides/categories.
// @Summary Create a guide category
// @Tags admin-guides
// @Accept json
// @Produce json
// @Success 201 {object} guideCategory
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /admin/guides/categories [post]
func (h *GuideHandler) CreateCategory(c *fiber.Ctx) error {
	var req categoryRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 80 {
		return badRequest(c, "name is required and must be at most 80 characters")
	}
	var cat guideCategory
	err := h.db.QueryRow(c.Context(),
		`INSERT INTO guide_categories (name, sort_order) VALUES ($1, $2) RETURNING id, name, sort_order`,
		req.Name, req.SortOrder).Scan(&cat.ID, &cat.Name, &cat.SortOrder)
	if err != nil {
		return internalError(c)
	}
	return c.Status(fiber.StatusCreated).JSON(cat)
}

// UpdateCategory handles PUT /api/v1/admin/guides/categories/:id.
// @Summary Rename or reorder a guide category
// @Tags admin-guides
// @Accept json
// @Produce json
// @Param id path string true "Category ID"
// @Success 200 {object} guideCategory
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/guides/categories/{id} [put]
func (h *GuideHandler) UpdateCategory(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "category not found"})
	}
	var req categoryRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 80 {
		return badRequest(c, "name is required and must be at most 80 characters")
	}
	var cat guideCategory
	err := h.db.QueryRow(c.Context(),
		`UPDATE guide_categories SET name = $1, sort_order = $2 WHERE id = $3 RETURNING id, name, sort_order`,
		req.Name, req.SortOrder, id).Scan(&cat.ID, &cat.Name, &cat.SortOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "category not found"})
	}
	if err != nil {
		return internalError(c)
	}
	return c.JSON(cat)
}

// DeleteCategory handles DELETE /api/v1/admin/guides/categories/:id. Guides in
// the category stay and become uncategorized.
// @Summary Delete a guide category
// @Tags admin-guides
// @Param id path string true "Category ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/guides/categories/{id} [delete]
func (h *GuideHandler) DeleteCategory(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "category not found"})
	}
	tag, err := h.db.Exec(c.Context(), `DELETE FROM guide_categories WHERE id = $1`, id)
	if err != nil {
		return internalError(c)
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "category not found"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// ListGuides handles GET /api/v1/admin/guides.
// @Summary List every guide, drafts included
// @Tags admin-guides
// @Produce json
// @Success 200 {array} adminGuide
// @Security BearerAuth
// @Router /admin/guides [get]
func (h *GuideHandler) ListGuides(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.Context(), `SELECT `+adminGuideColumns+` FROM guides ORDER BY sort_order, created_at`)
	if err != nil {
		return internalError(c)
	}
	defer rows.Close()
	out := make([]adminGuide, 0)
	for rows.Next() {
		g, err := scanAdminGuide(rows)
		if err != nil {
			return internalError(c)
		}
		out = append(out, g)
	}
	if rows.Err() != nil {
		return internalError(c)
	}
	return c.JSON(out)
}

// CreateGuide handles POST /api/v1/admin/guides.
// @Summary Create a guide
// @Description Markdown or HTML source; the server stores sanitized HTML alongside it
// @Tags admin-guides
// @Accept json
// @Produce json
// @Success 201 {object} adminGuide
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /admin/guides [post]
func (h *GuideHandler) CreateGuide(c *fiber.Ctx) error {
	var req guideRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	html, problem := validateGuide(&req)
	if problem != "" {
		return badRequest(c, problem)
	}
	g, err := scanAdminGuide(h.db.QueryRow(c.Context(),
		`INSERT INTO guides (category_id, title, summary, format, content, rendered_html, is_published, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+adminGuideColumns,
		req.CategoryID, req.Title, req.Summary, req.Format, req.Content, html, req.IsPublished, req.SortOrder))
	if isForeignKeyViolation(err) {
		return badRequest(c, "category not found")
	}
	if err != nil {
		return internalError(c)
	}
	return c.Status(fiber.StatusCreated).JSON(g)
}

// UpdateGuide handles PUT /api/v1/admin/guides/:id.
// @Summary Replace a guide
// @Tags admin-guides
// @Accept json
// @Produce json
// @Param id path string true "Guide ID"
// @Success 200 {object} adminGuide
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/guides/{id} [put]
func (h *GuideHandler) UpdateGuide(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "guide not found"})
	}
	var req guideRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	html, problem := validateGuide(&req)
	if problem != "" {
		return badRequest(c, problem)
	}
	g, err := scanAdminGuide(h.db.QueryRow(c.Context(),
		`UPDATE guides SET category_id = $1, title = $2, summary = $3, format = $4, content = $5,
		        rendered_html = $6, is_published = $7, sort_order = $8, updated_at = NOW()
		 WHERE id = $9 RETURNING `+adminGuideColumns,
		req.CategoryID, req.Title, req.Summary, req.Format, req.Content, html, req.IsPublished, req.SortOrder, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "guide not found"})
	}
	if isForeignKeyViolation(err) {
		return badRequest(c, "category not found")
	}
	if err != nil {
		return internalError(c)
	}
	return c.JSON(g)
}

// DeleteGuide handles DELETE /api/v1/admin/guides/:id.
// @Summary Delete a guide
// @Tags admin-guides
// @Param id path string true "Guide ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/guides/{id} [delete]
func (h *GuideHandler) DeleteGuide(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "guide not found"})
	}
	tag, err := h.db.Exec(c.Context(), `DELETE FROM guides WHERE id = $1`, id)
	if err != nil {
		return internalError(c)
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "guide not found"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// Preview handles POST /api/v1/admin/guides/preview: the editor's live view
// goes through the same renderer and sanitizer as a saved guide.
// @Summary Render guide source without saving
// @Tags admin-guides
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /admin/guides/preview [post]
func (h *GuideHandler) Preview(c *fiber.Ctx) error {
	var req struct {
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	if len(req.Content) > maxGuideContent {
		return badRequest(c, "content is too long")
	}
	html, err := services.RenderGuide(req.Format, req.Content)
	if err != nil {
		return badRequest(c, "format must be markdown or html")
	}
	return c.JSON(fiber.Map{"html": html})
}

type userGuideSummary struct {
	ID         string    `json:"id"`
	CategoryID *string   `json:"category_id"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ListPublished handles GET /api/v1/user/guides.
// @Summary List published guides with their categories
// @Tags user-portal
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /user/guides [get]
func (h *GuideHandler) ListPublished(c *fiber.Ctx) error {
	cats, err := h.categories(c)
	if err != nil {
		return internalError(c)
	}
	rows, err := h.db.Query(c.Context(),
		`SELECT id, category_id, title, summary, updated_at FROM guides
		 WHERE is_published ORDER BY sort_order, created_at`)
	if err != nil {
		return internalError(c)
	}
	defer rows.Close()
	guides := make([]userGuideSummary, 0)
	for rows.Next() {
		var g userGuideSummary
		if err := rows.Scan(&g.ID, &g.CategoryID, &g.Title, &g.Summary, &g.UpdatedAt); err != nil {
			return internalError(c)
		}
		guides = append(guides, g)
	}
	if rows.Err() != nil {
		return internalError(c)
	}
	return c.JSON(fiber.Map{"categories": cats, "guides": guides})
}

// GetPublished handles GET /api/v1/user/guides/:id. Drafts answer 404 so
// their existence does not leak.
// @Summary Read a published guide
// @Tags user-portal
// @Produce json
// @Param id path string true "Guide ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /user/guides/{id} [get]
func (h *GuideHandler) GetPublished(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "guide not found"})
	}
	var out struct {
		ID           string    `json:"id"`
		Title        string    `json:"title"`
		Summary      string    `json:"summary"`
		CategoryID   *string   `json:"category_id"`
		CategoryName *string   `json:"category_name"`
		RenderedHTML string    `json:"rendered_html"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	err := h.db.QueryRow(c.Context(),
		`SELECT g.id, g.title, g.summary, g.category_id, gc.name, g.rendered_html, g.updated_at
		 FROM guides g LEFT JOIN guide_categories gc ON gc.id = g.category_id
		 WHERE g.id = $1 AND g.is_published`, id).
		Scan(&out.ID, &out.Title, &out.Summary, &out.CategoryID, &out.CategoryName, &out.RenderedHTML, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "guide not found"})
	}
	if err != nil {
		return internalError(c)
	}
	return c.JSON(out)
}
