package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminPromotionHandler handles admin promotion code CRUD endpoints.
type AdminPromotionHandler struct {
	db *pgxpool.Pool
}

// NewAdminPromotionHandler creates a new AdminPromotionHandler.
func NewAdminPromotionHandler(db *pgxpool.Pool) *AdminPromotionHandler {
	return &AdminPromotionHandler{db: db}
}

type promotionRequest struct {
	Code                  string   `json:"code"`
	DiscountType          string   `json:"discount_type"`
	DiscountValue         int64    `json:"discount_value"`
	ValidFrom             string   `json:"valid_from"`
	ValidUntil            string   `json:"valid_until"`
	MinOrderCents         int64    `json:"min_order_cents"`
	MaxRedemptions        *int     `json:"max_redemptions"`
	MaxRedemptionsPerUser int      `json:"max_redemptions_per_user"`
	FirstPurchaseOnly     bool     `json:"first_purchase_only"`
	PlanIDs               []string `json:"plan_ids"`
	DurationDays          []int    `json:"duration_days"`
	AllowedUserIDs        []string `json:"allowed_user_ids"`
	IsActive              *bool    `json:"is_active"`
}

type promotionResponse struct {
	ID                    string    `json:"id"`
	Code                  string    `json:"code"`
	DiscountType          string    `json:"discount_type"`
	DiscountValue         int64     `json:"discount_value"`
	ValidFrom             time.Time `json:"valid_from"`
	ValidUntil            time.Time `json:"valid_until"`
	MinOrderCents         int64     `json:"min_order_cents"`
	MaxRedemptions        *int      `json:"max_redemptions"`
	MaxRedemptionsPerUser int       `json:"max_redemptions_per_user"`
	FirstPurchaseOnly     bool      `json:"first_purchase_only"`
	PlanIDs               []string  `json:"plan_ids"`
	DurationDays          []int     `json:"duration_days"`
	AllowedUserIDs        []string  `json:"allowed_user_ids"`
	RedeemedCount         int       `json:"redeemed_count"`
	IsActive              bool      `json:"is_active"`
	CreatedAt             time.Time `json:"created_at"`
}

const promotionSelectColumns = `id, code, discount_type, discount_value, valid_from, valid_until,
	min_order_cents, max_redemptions, max_redemptions_per_user, first_purchase_only,
	COALESCE(plan_ids, '{}'), COALESCE(duration_days, '{}'), COALESCE(allowed_user_ids, '{}'),
	redeemed_count, is_active, created_at`

func scanPromotion(row pgx.Row) (*promotionResponse, error) {
	var p promotionResponse
	if err := row.Scan(&p.ID, &p.Code, &p.DiscountType, &p.DiscountValue, &p.ValidFrom, &p.ValidUntil,
		&p.MinOrderCents, &p.MaxRedemptions, &p.MaxRedemptionsPerUser, &p.FirstPurchaseOnly,
		&p.PlanIDs, &p.DurationDays, &p.AllowedUserIDs, &p.RedeemedCount, &p.IsActive, &p.CreatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// List handles GET /api/v1/admin/promotions.
// @Summary List promotion codes
// @Description Returns every promotion code, newest first
// @Tags admin-promotions
// @Produce json
// @Success 200 {array} promotionResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/promotions [get]
func (h *AdminPromotionHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(context.Background(),
		`SELECT `+promotionSelectColumns+` FROM promotion_codes ORDER BY created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	defer rows.Close()

	results := make([]promotionResponse, 0)
	for rows.Next() {
		p, err := scanPromotion(rows)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
		}
		results = append(results, *p)
	}
	return c.JSON(results)
}

// Create handles POST /api/v1/admin/promotions.
// @Summary Create a promotion code
// @Description Creates a promotion code with a discount, validity window, and eligibility rules
// @Tags admin-promotions
// @Accept json
// @Produce json
// @Param body body promotionRequest true "Promotion code fields"
// @Success 201 {object} promotionResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/promotions [post]
func (h *AdminPromotionHandler) Create(c *fiber.Ctx) error {
	var req promotionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if err := validatePromotionRequest(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	validFrom, validUntil, err := parsePromotionWindow(req.ValidFrom, req.ValidUntil)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	row := h.db.QueryRow(context.Background(),
		`INSERT INTO promotion_codes
		   (code, discount_type, discount_value, valid_from, valid_until, min_order_cents,
		    max_redemptions, max_redemptions_per_user, first_purchase_only,
		    plan_ids, duration_days, allowed_user_ids)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING `+promotionSelectColumns,
		req.Code, req.DiscountType, req.DiscountValue, validFrom, validUntil, req.MinOrderCents,
		req.MaxRedemptions, defaultPerUserCap(req.MaxRedemptionsPerUser), req.FirstPurchaseOnly,
		nullableStringArray(req.PlanIDs), nullableIntArray(req.DurationDays), nullableStringArray(req.AllowedUserIDs),
	)
	p, err := scanPromotion(row)
	if err != nil {
		if isUniqueViolation(err) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "code already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	return c.Status(fiber.StatusCreated).JSON(p)
}

// Update handles PUT /api/v1/admin/promotions/:id.
// @Summary Update a promotion code
// @Description Replaces a promotion code's fields
// @Tags admin-promotions
// @Accept json
// @Produce json
// @Param id path string true "Promotion ID"
// @Param body body promotionRequest true "Promotion code fields"
// @Success 200 {object} promotionResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/promotions/{id} [put]
func (h *AdminPromotionHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req promotionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if err := validatePromotionRequest(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	validFrom, validUntil, err := parsePromotionWindow(req.ValidFrom, req.ValidUntil)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	row := h.db.QueryRow(context.Background(),
		`UPDATE promotion_codes SET
		   code = $2, discount_type = $3, discount_value = $4, valid_from = $5, valid_until = $6,
		   min_order_cents = $7, max_redemptions = $8, max_redemptions_per_user = $9,
		   first_purchase_only = $10, plan_ids = $11, duration_days = $12, allowed_user_ids = $13,
		   is_active = $14
		 WHERE id = $1
		 RETURNING `+promotionSelectColumns,
		id, req.Code, req.DiscountType, req.DiscountValue, validFrom, validUntil, req.MinOrderCents,
		req.MaxRedemptions, defaultPerUserCap(req.MaxRedemptionsPerUser), req.FirstPurchaseOnly,
		nullableStringArray(req.PlanIDs), nullableIntArray(req.DurationDays), nullableStringArray(req.AllowedUserIDs),
		isActive,
	)
	p, err := scanPromotion(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "promotion not found"})
		}
		if isUniqueViolation(err) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "code already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}

	return c.JSON(p)
}

// Delete handles DELETE /api/v1/admin/promotions/:id.
// @Summary Delete a promotion code
// @Description Deletes a promotion code. Orders that already reserved it keep their discount snapshot.
// @Tags admin-promotions
// @Produce json
// @Param id path string true "Promotion ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/promotions/{id} [delete]
func (h *AdminPromotionHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	tag, err := h.db.Exec(context.Background(), `DELETE FROM promotion_codes WHERE id = $1`, id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
	if tag.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "promotion not found"})
	}

	return c.JSON(fiber.Map{"message": "promotion deleted"})
}

func defaultPerUserCap(v int) int {
	if v <= 0 {
		return 1
	}
	return v
}

func nullableStringArray(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func nullableIntArray(v []int) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// validatePromotionRequest checks the fields that would otherwise surface
// only as an opaque CHECK-constraint violation from Postgres - discount_type/
// discount_value's own bounds, and window ordering, all restated here so a
// bad request gets a specific 400 rather than a generic 500.
func validatePromotionRequest(req *promotionRequest) error {
	if req.Code == "" {
		return errors.New("code is required")
	}
	switch req.DiscountType {
	case "percent":
		if req.DiscountValue <= 0 || req.DiscountValue > 100 {
			return errors.New("discount_value must be between 1 and 100 for a percent discount")
		}
	case "fixed":
		if req.DiscountValue <= 0 {
			return errors.New("discount_value must be positive for a fixed discount")
		}
	default:
		return errors.New("discount_type must be 'percent' or 'fixed'")
	}
	if req.MinOrderCents < 0 {
		return errors.New("min_order_cents cannot be negative")
	}
	if req.MaxRedemptions != nil && *req.MaxRedemptions <= 0 {
		return errors.New("max_redemptions must be positive when set")
	}
	return nil
}

// parsePromotionWindow parses the RFC3339 validity window and checks
// ordering before it ever reaches the database's own CHECK constraint, so a
// malformed or backwards window is a 400 with a clear message.
func parsePromotionWindow(fromRaw, untilRaw string) (time.Time, time.Time, error) {
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("valid_from must be RFC3339: %w", err)
	}
	until, err := time.Parse(time.RFC3339, untilRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("valid_until must be RFC3339: %w", err)
	}
	if !until.After(from) {
		return time.Time{}, time.Time{}, errors.New("valid_until must be after valid_from")
	}
	return from, until, nil
}
