package handlers

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminAffiliateHandler struct{ db *pgxpool.Pool }

func NewAdminAffiliateHandler(db *pgxpool.Pool) *AdminAffiliateHandler {
	return &AdminAffiliateHandler{db: db}
}

type affiliateRequest struct {
	Name           string  `json:"name"`
	Code           string  `json:"code"`
	CommissionRate float64 `json:"commission_rate"`
	DiscountType   string  `json:"discount_type"`
	DiscountValue  int64   `json:"discount_value"`
	IsActive       *bool   `json:"is_active"`
}

type affiliateResponse struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Code           string    `json:"code"`
	CommissionRate float64   `json:"commission_rate"`
	DiscountType   string    `json:"discount_type"`
	DiscountValue  int64     `json:"discount_value"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
}

const affiliateColumns = `id, name, code, commission_rate::float8, discount_type, discount_value, is_active, created_at`

func scanAffiliate(row pgx.Row) (affiliateResponse, error) {
	var p affiliateResponse
	err := row.Scan(&p.ID, &p.Name, &p.Code, &p.CommissionRate, &p.DiscountType, &p.DiscountValue, &p.IsActive, &p.CreatedAt)
	return p, err
}

func (h *AdminAffiliateHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(), `SELECT `+affiliateColumns+` FROM affiliate_partners ORDER BY created_at DESC, id`)
	if err != nil {
		return promotionKindError(c, err)
	}
	defer rows.Close()
	result := make([]affiliateResponse, 0)
	for rows.Next() {
		p, err := scanAffiliate(rows)
		if err != nil {
			return promotionKindError(c, err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return promotionKindError(c, err)
	}
	return c.JSON(result)
}

func validateAffiliate(req *affiliateRequest) error {
	req.Name, req.Code = strings.TrimSpace(req.Name), strings.TrimSpace(req.Code)
	if req.Name == "" || req.Code == "" {
		return errors.New("name and code are required")
	}
	if math.IsNaN(req.CommissionRate) || math.IsInf(req.CommissionRate, 0) || req.CommissionRate < 0 || req.CommissionRate > 100 {
		return errors.New("commission_rate must be between 0 and 100")
	}
	if req.DiscountType != "percent" && req.DiscountType != "fixed" {
		return errors.New("discount_type must be percent or fixed")
	}
	if req.DiscountValue <= 0 || (req.DiscountType == "percent" && req.DiscountValue > 100) {
		return errors.New("invalid discount_value")
	}
	return nil
}

func (h *AdminAffiliateHandler) Create(c *fiber.Ctx) error { return h.save(c, false) }
func (h *AdminAffiliateHandler) Update(c *fiber.Ctx) error { return h.save(c, true) }

func (h *AdminAffiliateHandler) save(c *fiber.Ctx, update bool) error {
	if update && !promotionKindID(c, "id") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}
	var req affiliateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if err := validateAffiliate(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	args := []any{req.Name, req.Code, req.CommissionRate, req.DiscountType, req.DiscountValue, defaultTrue(req.IsActive)}
	query := `INSERT INTO affiliate_partners (name,code,commission_rate,discount_type,discount_value,is_active)
		VALUES ($1,$2,$3,$4,$5,$6)`
	status := fiber.StatusCreated
	if update {
		query = `UPDATE affiliate_partners SET name=$1,code=$2,commission_rate=$3,discount_type=$4,
			discount_value=$5,is_active=$6 WHERE id=$7`
		args = append(args, c.Params("id"))
		status = fiber.StatusOK
	}
	p, err := scanAffiliate(h.db.QueryRow(c.UserContext(), query+` RETURNING `+affiliateColumns, args...))
	if err != nil {
		return promotionKindError(c, err)
	}
	return c.Status(status).JSON(p)
}

func (h *AdminAffiliateHandler) Delete(c *fiber.Ctx) error {
	if !promotionKindID(c, "id") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}
	tag, err := h.db.Exec(c.UserContext(), `DELETE FROM affiliate_partners WHERE id=$1`, c.Params("id"))
	if err != nil {
		return promotionKindError(c, err)
	}
	if tag.RowsAffected() == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "partner not found"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}
