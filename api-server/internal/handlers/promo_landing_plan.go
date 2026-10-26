package handlers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type promoLandingPlan struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	DurationDays int               `json:"duration_days"`
	PriceCents   int64             `json:"price_cents"`
	TrafficLimit *int64            `json:"traffic_limit"`
	SpeedLimit   *int              `json:"speed_limit"`
	MaxDevices   int               `json:"max_devices"`
	Features     []userPlanFeature `json:"features"`
	Prices       []userPlanPrice   `json:"prices"`
}

// landingPlan picks the plan the offer card prices: the trial's own plan, or
// for discounts the first advertised plan the code may apply to, at its
// shortest priced duration so "was / now" matches what checkout will charge.
func (h *PromoLandingHandler) landingPlan(ctx context.Context, planID *string, eligiblePlans, eligibleDurations []string, lang string, minimum int64) (*promoLandingPlan, error) {
	var p promoLandingPlan
	err := h.db.QueryRow(ctx,
		`SELECT p.id, p.name, pp.duration_days, pp.price_cents, p.traffic_limit, p.speed_limit, p.max_devices
		 FROM plans p
		 JOIN plan_prices pp ON pp.plan_id = p.id
		 WHERE p.is_active
		   AND pp.price_cents >= $4
		   AND (COALESCE(cardinality($3::int[]),0) = 0 OR pp.duration_days = ANY($3::int[]))
		   AND ($1::uuid IS NOT NULL AND p.id = $1::uuid
		        OR $1::uuid IS NULL AND p.advertise AND p.is_advertised
		           AND (COALESCE(cardinality($2::uuid[]),0) = 0 OR p.id = ANY($2::uuid[])))
		 ORDER BY pp.duration_days, pp.price_cents
		 LIMIT 1`,
		planID, eligiblePlans, eligibleDurations, minimum,
	).Scan(&p.ID, &p.Name, &p.DurationDays, &p.PriceCents, &p.TrafficLimit, &p.SpeedLimit, &p.MaxDevices)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Prices = make([]userPlanPrice, 0)
	prices, err := h.db.Query(ctx, `SELECT duration_days,price_cents FROM plan_prices
		WHERE plan_id=$1 AND price_cents >= $3
		AND (COALESCE(cardinality($2::int[]),0)=0 OR duration_days=ANY($2::int[]))
		ORDER BY duration_days`, p.ID, eligibleDurations, minimum)
	if err != nil {
		return nil, err
	}
	defer prices.Close()
	for prices.Next() {
		var price userPlanPrice
		if err := prices.Scan(&price.DurationDays, &price.PriceCents); err != nil {
			return nil, err
		}
		p.Prices = append(p.Prices, price)
	}
	if err := prices.Err(); err != nil {
		return nil, err
	}
	p.Features = make([]userPlanFeature, 0)
	rows, err := h.db.Query(ctx,
		`SELECT pf.included, COALESCE(t.text, en.text)
		 FROM plan_features pf
		 LEFT JOIN plan_feature_texts t ON t.plan_id = pf.plan_id AND t.position = pf.position AND t.language = $2
		 LEFT JOIN plan_feature_texts en ON en.plan_id = pf.plan_id AND en.position = pf.position AND en.language = 'en'
		 WHERE pf.plan_id = $1 AND COALESCE(t.text, en.text) IS NOT NULL
		 ORDER BY pf.position`, p.ID, lang,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f userPlanFeature
		if err := rows.Scan(&f.Included, &f.Text); err != nil {
			return nil, err
		}
		p.Features = append(p.Features, f)
	}
	return &p, rows.Err()
}
