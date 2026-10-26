package handlers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PromoLandingHandler struct{ db *pgxpool.Pool }

func NewPromoLandingHandler(db *pgxpool.Pool) *PromoLandingHandler {
	return &PromoLandingHandler{db: db}
}

type promoLandingResponse struct {
	Type                string  `json:"type"`
	Code                string  `json:"code"`
	DiscountDescription string  `json:"discount_description"`
	PlanName            *string `json:"plan_name,omitempty"`
	TrialDays           *int    `json:"trial_days,omitempty"`
	// Structured benefit so the page can phrase it in the visitor's language;
	// discount_description stays for existing clients.
	DiscountType  *string    `json:"discount_type,omitempty"`
	DiscountValue *int64     `json:"discount_value,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	IsValid       bool       `json:"is_valid"`
}

// Get exposes only public benefit information, never eligibility lists or partner commissions.
// Codes in older coupon campaigns take precedence when namespaces overlap.
func (h *PromoLandingHandler) Get(c *fiber.Ctx) error {
	code := strings.TrimSpace(c.Params("code"))
	var result promoLandingResponse
	var discountType string
	var value int64
	var validUntil time.Time
	err := h.db.QueryRow(c.UserContext(), `SELECT code,discount_type,discount_value,valid_until,
		is_active AND NOW() >= valid_from AND NOW() < valid_until
		AND (max_redemptions IS NULL OR redeemed_count < max_redemptions)
		FROM promotion_codes WHERE code=$1`, code).Scan(&result.Code, &discountType, &value, &validUntil, &result.IsValid)
	if err == nil {
		result.Type = "coupon"
		result.DiscountDescription = publicDiscountDescription(discountType, value)
		result.DiscountType, result.DiscountValue, result.ValidUntil = &discountType, &value, &validUntil
		return c.JSON(result)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return promotionKindError(c, err)
	}
	var days int
	err = h.db.QueryRow(c.UserContext(), `SELECT t.code,p.name,t.trial_days,
		t.is_active AND (t.plan_id IS NULL OR COALESCE(p.is_active,false))
		FROM free_trial_promotions t LEFT JOIN plans p ON p.id=t.plan_id WHERE t.code=$1`, code).
		Scan(&result.Code, &result.PlanName, &days, &result.IsValid)
	if err == nil {
		result.Type, result.TrialDays = "trial", &days
		result.DiscountDescription = fmt.Sprintf("%d-day free trial", days)
		return c.JSON(result)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return promotionKindError(c, err)
	}
	err = h.db.QueryRow(c.UserContext(), `SELECT code,discount_type,discount_value,is_active
		FROM affiliate_partners WHERE code=$1`, code).Scan(&result.Code, &discountType, &value, &result.IsValid)
	if err == nil {
		result.Type = "affiliate"
		result.DiscountDescription = publicDiscountDescription(discountType, value)
		result.DiscountType, result.DiscountValue = &discountType, &value
		return c.JSON(result)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return promotionKindError(c, err)
	}
	return c.Status(404).JSON(promoLandingResponse{Code: code, IsValid: false})
}

func publicDiscountDescription(kind string, value int64) string {
	if kind == "percent" {
		return fmt.Sprintf("%d%% off", value)
	}
	return fmt.Sprintf("USD %d.%02d off", value/100, value%100)
}
