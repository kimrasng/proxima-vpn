package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserSubscriptionDomainHandler serves the public domain pool to authenticated users.
type UserSubscriptionDomainHandler struct {
	db *pgxpool.Pool
}

// NewUserSubscriptionDomainHandler returns a new handler.
func NewUserSubscriptionDomainHandler(db *pgxpool.Pool) *UserSubscriptionDomainHandler {
	return &UserSubscriptionDomainHandler{db: db}
}

type publicDomainResponse struct {
	ID           string    `json:"id"`
	Domain       string    `json:"domain"`
	DisplayOrder int       `json:"display_order"`
	IsDefault    bool      `json:"is_default"`
	CreatedAt    time.Time `json:"created_at"`
}

// List returns every enabled, public subscription domain.
// @Summary List public subscription domains
// @Tags user/subscription-domains
// @Produce json
// @Security BearerAuth
// @Success 200 {array} publicDomainResponse
// @Router /api/v1/user/subscription-domains [get]
func (h *UserSubscriptionDomainHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(context.Background(),
		`SELECT id, domain, sort_order, is_default, created_at
		 FROM subscription_domains
		 WHERE is_enabled AND is_public
		 ORDER BY sort_order, created_at`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list domains"})
	}
	defer rows.Close()

	out := make([]publicDomainResponse, 0)
	for rows.Next() {
		var d publicDomainResponse
		if err := rows.Scan(&d.ID, &d.Domain, &d.DisplayOrder, &d.IsDefault, &d.CreatedAt); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to scan domain"})
		}
		out = append(out, d)
	}
	return c.JSON(out)
}
