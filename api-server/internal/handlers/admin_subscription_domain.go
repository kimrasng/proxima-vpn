package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminSubscriptionDomainHandler manages the subscription domain pool.
type AdminSubscriptionDomainHandler struct {
	db *pgxpool.Pool
}

// NewAdminSubscriptionDomainHandler returns a new handler.
func NewAdminSubscriptionDomainHandler(db *pgxpool.Pool) *AdminSubscriptionDomainHandler {
	return &AdminSubscriptionDomainHandler{db: db}
}

// --- request / response types ---

type createDomainRequest struct {
	Domain       string `json:"domain"`
	Enabled      *bool  `json:"enabled"`
	IsPublic     *bool  `json:"is_public"`
	DisplayOrder *int   `json:"display_order"`
	IsDefault    *bool  `json:"is_default"`
}

type updateDomainRequest struct {
	Domain       *string `json:"domain"`
	Enabled      *bool   `json:"enabled"`
	IsPublic     *bool   `json:"is_public"`
	DisplayOrder *int    `json:"display_order"`
	IsDefault    *bool   `json:"is_default"`
}

type domainResponse struct {
	ID           string    `json:"id"`
	Domain       string    `json:"domain"`
	Enabled      bool      `json:"enabled"`
	IsPublic     bool      `json:"is_public"`
	DisplayOrder int       `json:"display_order"`
	IsDefault    bool      `json:"is_default"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// --- CRUD ---

// List returns all subscription domains ordered by sort_order.
// @Summary List subscription domains
// @Tags admin/subscription-domains
// @Produce json
// @Security BearerAuth
// @Success 200 {array} domainResponse
// @Router /api/v1/admin/subscription-domains [get]
func (h *AdminSubscriptionDomainHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(context.Background(),
		`SELECT id, domain, is_enabled, is_public, sort_order, is_default, created_at, updated_at
		 FROM subscription_domains
		 ORDER BY sort_order, created_at`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list domains"})
	}
	defer rows.Close()

	out := make([]domainResponse, 0)
	for rows.Next() {
		var d domainResponse
		if err := rows.Scan(&d.ID, &d.Domain, &d.Enabled, &d.IsPublic, &d.DisplayOrder, &d.IsDefault, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to scan domain"})
		}
		out = append(out, d)
	}
	return c.JSON(out)
}

// Create adds a new subscription domain.
// @Summary Create subscription domain
// @Tags admin/subscription-domains
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body createDomainRequest true "domain"
// @Success 201 {object} domainResponse
// @Router /api/v1/admin/subscription-domains [post]
func (h *AdminSubscriptionDomainHandler) Create(c *fiber.Ctx) error {
	var req createDomainRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	domain, err := parseSubscriptionHostname(req.Domain)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	isPublic := true
	if req.IsPublic != nil {
		isPublic = *req.IsPublic
	}
	displayOrder := 0
	if req.DisplayOrder != nil {
		displayOrder = *req.DisplayOrder
	}
	isDefault := false
	if req.IsDefault != nil {
		isDefault = *req.IsDefault
	}

	ctx := context.Background()

	tx, err := beginDomainMutation(ctx, h.db, isDefault)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to prepare domain creation"})
	}
	defer tx.Rollback(ctx)

	var d domainResponse
	err = tx.QueryRow(ctx,
		`INSERT INTO subscription_domains (domain, is_enabled, is_public, sort_order, is_default)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, domain, is_enabled, is_public, sort_order, is_default, created_at, updated_at`,
		domain, enabled, isPublic, displayOrder, isDefault,
	).Scan(&d.ID, &d.Domain, &d.Enabled, &d.IsPublic, &d.DisplayOrder, &d.IsDefault, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "domain already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create domain"})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create domain"})
	}
	return c.Status(fiber.StatusCreated).JSON(d)
}

// Update modifies a subscription domain.
// @Summary Update subscription domain
// @Tags admin/subscription-domains
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "domain id"
// @Param body body updateDomainRequest true "fields"
// @Success 200 {object} domainResponse
// @Router /api/v1/admin/subscription-domains/{id} [put]
func (h *AdminSubscriptionDomainHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	var req updateDomainRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	ctx := context.Background()

	setClauses := []string{"updated_at = NOW()"}
	args := []any{}
	argIdx := 1

	if req.Domain != nil {
		d, err := parseSubscriptionHostname(*req.Domain)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		setClauses = append(setClauses, fmt.Sprintf("domain = $%d", argIdx))
		args = append(args, d)
		argIdx++
	}
	if req.Enabled != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_enabled = $%d", argIdx))
		args = append(args, *req.Enabled)
		argIdx++
	}
	if req.IsPublic != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_public = $%d", argIdx))
		args = append(args, *req.IsPublic)
		argIdx++
	}
	if req.DisplayOrder != nil {
		setClauses = append(setClauses, fmt.Sprintf("sort_order = $%d", argIdx))
		args = append(args, *req.DisplayOrder)
		argIdx++
	}
	if req.IsDefault != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_default = $%d", argIdx))
		args = append(args, *req.IsDefault)
		argIdx++
	}

	query := fmt.Sprintf(
		"UPDATE subscription_domains SET %s WHERE id = $%d RETURNING id, domain, is_enabled, is_public, sort_order, is_default, created_at, updated_at",
		strings.Join(setClauses, ", "), argIdx,
	)
	args = append(args, id)

	selectDefault := req.IsDefault != nil && *req.IsDefault
	tx, err := beginDomainMutation(ctx, h.db, selectDefault)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to prepare domain update"})
	}
	defer tx.Rollback(ctx)

	var d domainResponse
	err = tx.QueryRow(ctx, query, args...).Scan(
		&d.ID, &d.Domain, &d.Enabled, &d.IsPublic, &d.DisplayOrder, &d.IsDefault, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
		}
		if strings.Contains(err.Error(), "duplicate key") {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "domain already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update domain"})
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update domain"})
	}
	return c.JSON(d)
}

// Delete removes a subscription domain.
// @Summary Delete subscription domain
// @Tags admin/subscription-domains
// @Security BearerAuth
// @Param id path string true "domain id"
// @Success 200
// @Router /api/v1/admin/subscription-domains/{id} [delete]
func (h *AdminSubscriptionDomainHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	result, err := h.db.Exec(context.Background(), `DELETE FROM subscription_domains WHERE id = $1`, id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete domain"})
	}
	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}
	return c.JSON(fiber.Map{"message": "domain deleted"})
}
