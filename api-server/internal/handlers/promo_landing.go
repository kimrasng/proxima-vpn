package handlers

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
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
	RequiresCard        *bool   `json:"requires_card,omitempty"`
	MinOrderCents       int64   `json:"min_order_cents,omitempty"`
	// Structured benefit so the page can phrase it in the visitor's language;
	// discount_description stays for existing clients.
	DiscountType     *string    `json:"discount_type,omitempty"`
	DiscountValue    *int64     `json:"discount_value,omitempty"`
	ValidUntil       *time.Time `json:"valid_until,omitempty"`
	IsValid          bool       `json:"is_valid"`
	IsEligible       bool       `json:"is_eligible"`
	IneligibleReason string     `json:"ineligible_reason,omitempty"`
	// Plan priced on the offer card; nil when no advertised plan qualifies.
	Plan *promoLandingPlan `json:"plan,omitempty"`
	// Admin-authored copy for this code; empty fields fall back to defaults.
	Headline    string `json:"headline,omitempty"`
	Description string `json:"description,omitempty"`
	CTALabel    string `json:"cta_label,omitempty"`
}

// Get exposes only public benefit information, never eligibility lists or partner commissions.
// Codes in older coupon campaigns take precedence when namespaces overlap.
func (h *PromoLandingHandler) Get(c *fiber.Ctx) error {
	code := strings.TrimSpace(c.Params("code"))
	lang := c.Query("lang", "en")
	var result promoLandingResponse
	var eligiblePlans, eligibleDurations []string
	var trialPlanID *string
	var discountType string
	var value int64
	var validUntil time.Time
	err := h.db.QueryRow(c.UserContext(), `SELECT code,discount_type,discount_value,valid_until,
		is_active AND NOW() >= valid_from AND NOW() < valid_until
		AND (max_redemptions IS NULL OR redeemed_count < max_redemptions),
		COALESCE(plan_ids::text[], '{}'), COALESCE(duration_days::text[], '{}'),min_order_cents
		FROM promotion_codes WHERE code=$1`, code).Scan(&result.Code, &discountType, &value, &validUntil, &result.IsValid, &eligiblePlans, &eligibleDurations, &result.MinOrderCents)
	if err == nil {
		result.Type = "coupon"
		result.DiscountDescription = publicDiscountDescription(discountType, value)
		result.DiscountType, result.DiscountValue, result.ValidUntil = &discountType, &value, &validUntil
		return h.withPlan(c, result, nil, eligiblePlans, eligibleDurations, lang)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return promotionKindError(c, err)
	}
	var days, duration int
	err = h.db.QueryRow(c.UserContext(), `SELECT t.code,p.name,t.trial_days,
		t.is_active AND (t.plan_id IS NULL OR COALESCE(p.is_active,false)), t.plan_id::text,t.duration_days,t.requires_card
		FROM free_trial_promotions t LEFT JOIN plans p ON p.id=t.plan_id WHERE t.code=$1`, code).
		Scan(&result.Code, &result.PlanName, &days, &result.IsValid, &trialPlanID, &duration, &result.RequiresCard)
	if err == nil {
		result.Type, result.TrialDays = "trial", &days
		result.DiscountDescription = fmt.Sprintf("%d-day free trial", days)
		return h.withPlan(c, result, trialPlanID, nil, []string{fmt.Sprint(duration)}, lang)
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
		return h.withPlan(c, result, nil, nil, nil, lang)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return promotionKindError(c, err)
	}
	return c.Status(404).JSON(promoLandingResponse{Code: code, IsValid: false})
}

func (h *PromoLandingHandler) withPlan(c *fiber.Ctx, result promoLandingResponse, planID *string, plans, durations []string, lang string) error {
	var pagePlan *string
	err := h.db.QueryRow(c.UserContext(),
		`SELECT headline, description, cta_label, plan_id::text FROM promo_landing_pages WHERE code = $1`, result.Code,
	).Scan(&result.Headline, &result.Description, &result.CTALabel, &pagePlan)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return promotionKindError(c, err)
	}
	if pagePlan != nil && planID == nil {
		if len(plans) > 0 {
			allowed := false
			for _, id := range plans {
				if id == *pagePlan {
					allowed = true
					break
				}
			}
			if !allowed {
				result.IsValid = false
			}
		}
		plans = []string{*pagePlan}
	}
	visitor := promoVisitor(c)
	if userID, ok := c.Locals("user_id").(string); ok {
		if err := h.db.QueryRow(c.UserContext(), `SELECT email FROM users WHERE id=$1`, userID).Scan(&visitor.Email); err != nil {
			return promotionKindError(c, err)
		}
		if result.Type == "coupon" {
			if err := h.db.QueryRow(c.UserContext(), `SELECT CASE
				WHEN p.first_purchase_only AND EXISTS(SELECT 1 FROM plan_orders WHERE user_id=$2 AND status='paid')
					THEN 'not_first_purchase'
				WHEN COALESCE(cardinality(p.allowed_user_ids),0)>0 AND NOT ($2::uuid=ANY(p.allowed_user_ids))
					THEN 'user_not_allowed'
				WHEN (SELECT COUNT(*) FROM promotion_redemptions r WHERE r.promotion_id=p.id
					AND r.user_id=$2 AND r.status='confirmed')>=p.max_redemptions_per_user
					THEN 'user_cap_reached'
				ELSE '' END FROM promotion_codes p WHERE p.code=$1`,
				result.Code, userID).Scan(&result.IneligibleReason); err != nil {
				return promotionKindError(c, err)
			}
		}
		if result.Type == "trial" {
			if err := h.db.QueryRow(c.UserContext(), `SELECT CASE
				WHEN COALESCE(u.plan_expires_at>NOW(),false) THEN 'active_plan_exists'
				WHEN (SELECT COUNT(*) FROM (
					SELECT user_id FROM free_trial_redemptions WHERE promotion_id=t.id
					UNION ALL SELECT user_id FROM stripe_trial_subscriptions WHERE promotion_id=t.id
					AND status NOT IN ('expired','checkout')) claims WHERE claims.user_id=u.id)>=t.max_redemptions_per_user
					THEN 'user_cap_reached'
				WHEN t.max_redemptions IS NOT NULL AND (SELECT COUNT(*) FROM (
					SELECT user_id FROM free_trial_redemptions WHERE promotion_id=t.id
					UNION ALL SELECT user_id FROM stripe_trial_subscriptions WHERE promotion_id=t.id
					AND status<>'expired') claims)>=t.max_redemptions THEN 'overall_cap_reached'
				ELSE '' END FROM free_trial_promotions t JOIN users u ON u.id=$2 WHERE t.code=$1`,
				result.Code, userID).Scan(&result.IneligibleReason); err != nil {
				return promotionKindError(c, err)
			}
		}
		if result.IneligibleReason != "" {
			result.IsValid = false
		}
	}
	if err := services.CheckPromoEligibility(c.UserContext(), h.db, result.Code, visitor); err != nil {
		var reject *services.PromotionRejectError
		if !errors.As(err, &reject) {
			return promotionKindError(c, err)
		}
		result.IsValid = false
		result.IneligibleReason = string(reject.Reason)
	}
	plan, err := h.landingPlan(c.UserContext(), planID, plans, durations, lang, result.MinOrderCents)
	if err != nil {
		return promotionKindError(c, err)
	}
	result.Plan = plan
	result.IsEligible = result.IsValid && plan != nil
	if !result.IsEligible && result.IneligibleReason == "" {
		result.IneligibleReason = "not_eligible"
	}
	return c.JSON(result)
}

func publicDiscountDescription(kind string, value int64) string {
	if kind == "percent" {
		return fmt.Sprintf("%d%% off", value)
	}
	return fmt.Sprintf("USD %d.%02d off", value/100, value%100)
}
