package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PromotionCode is a promotion_codes row, as read back for validation and for
// the admin CRUD surface.
type PromotionCode struct {
	ID                    string
	Code                  string
	DiscountType          string // "percent" | "fixed"
	DiscountValue         int64
	ValidFrom             time.Time
	ValidUntil            time.Time
	MinOrderCents         int64
	MaxRedemptions        *int
	MaxRedemptionsPerUser int
	FirstPurchaseOnly     bool
	PlanIDs               []string
	DurationDays          []int
	AllowedUserIDs        []string
	RedeemedCount         int
	IsActive              bool
	CreatedAt             time.Time
}

// PromotionRejectReason classifies why Reserve refused a code, so a handler
// can return the specific reason to the caller instead of a generic error.
type PromotionRejectReason string

const (
	RejectNotFound          PromotionRejectReason = "not_found"
	RejectInactive          PromotionRejectReason = "inactive"
	RejectOutsideWindow     PromotionRejectReason = "outside_window"
	RejectPlanNotEligible   PromotionRejectReason = "plan_not_eligible"
	RejectBelowMinimum      PromotionRejectReason = "below_minimum"
	RejectNotFirstPurchase  PromotionRejectReason = "not_first_purchase"
	RejectUserNotAllowed    PromotionRejectReason = "user_not_allowed"
	RejectOverallCapReached PromotionRejectReason = "overall_cap_reached"
	RejectUserCapReached    PromotionRejectReason = "user_cap_reached"
)

// PromotionRejectError is returned by Reserve when a code fails an
// eligibility check. It is never a database/system error - a handler treats
// it as a 400/409 with Reason as the machine-readable body field.
type PromotionRejectError struct {
	Reason PromotionRejectReason
}

func (e *PromotionRejectError) Error() string {
	return fmt.Sprintf("promotion rejected: %s", e.Reason)
}

// PromotionReservation is what Reserve hands back on success: the discount
// to apply to the order being placed, and the code's own id to snapshot onto
// plan_orders.promotion_id.
type PromotionReservation struct {
	PromotionID   string
	DiscountCents int64
}

// PromotionService owns promotion_codes and the atomic redemption ledger in
// promotion_redemptions. Every check Reserve performs and the row it inserts
// happen inside one transaction, so a code's overall and per-user caps hold
// exactly under concurrent checkout - two requests racing to redeem the last
// slot cannot both succeed, because the capacity UPDATE below only matches
// (and locks) the row for the request that observes it as still available.
type PromotionService struct {
	db *pgxpool.Pool
}

// NewPromotionService creates a PromotionService.
func NewPromotionService(db *pgxpool.Pool) *PromotionService {
	return &PromotionService{db: db}
}

// Reserve validates code against userID's order (planID/durationDays/
// orderCents already known, since the price - and therefore the discount -
// depends on which plan and duration the order is for) and, if every check
// passes, atomically reserves one redemption slot and inserts the
// promotion_redemptions row backing orderID. Returns a *PromotionRejectError
// for any failed eligibility check; any other error is a system error.
//
// The overall cap is enforced by a single conditional UPDATE
// (redeemed_count = redeemed_count + 1 WHERE redeemed_count < max_redemptions
// OR max_redemptions IS NULL) - Postgres's row lock on that UPDATE is what
// makes two concurrent Reserve calls for the same code serialize on this one
// row rather than both reading a stale count and both proceeding. The
// per-user cap is checked immediately before, inside the same transaction,
// by counting non-released promotion_redemptions rows for (promotion_id,
// user_id) FOR UPDATE - the FOR UPDATE is needed because a second concurrent
// order from the same user must not read the count before the first one's
// insert commits.
func (s *PromotionService) Reserve(ctx context.Context, code string, userID, orderID, planID string, durationDays int, orderCents int64, isFirstPurchase bool) (*PromotionReservation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reserve: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var p PromotionCode
	var planIDs, allowedUserIDs []string
	var durationDaysArr []int
	err = tx.QueryRow(ctx,
		`SELECT id, discount_type, discount_value, valid_from, valid_until,
		        min_order_cents, max_redemptions, max_redemptions_per_user,
		        first_purchase_only, COALESCE(plan_ids, '{}'), COALESCE(duration_days, '{}'),
		        COALESCE(allowed_user_ids, '{}'), redeemed_count, is_active
		 FROM promotion_codes WHERE code = $1 FOR UPDATE`,
		code,
	).Scan(&p.ID, &p.DiscountType, &p.DiscountValue, &p.ValidFrom, &p.ValidUntil,
		&p.MinOrderCents, &p.MaxRedemptions, &p.MaxRedemptionsPerUser,
		&p.FirstPurchaseOnly, &planIDs, &durationDaysArr,
		&allowedUserIDs, &p.RedeemedCount, &p.IsActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &PromotionRejectError{Reason: RejectNotFound}
		}
		return nil, fmt.Errorf("load promotion: %w", err)
	}

	if !p.IsActive {
		return nil, &PromotionRejectError{Reason: RejectInactive}
	}
	now := time.Now()
	if now.Before(p.ValidFrom) || now.After(p.ValidUntil) {
		return nil, &PromotionRejectError{Reason: RejectOutsideWindow}
	}
	if len(planIDs) > 0 && !containsString(planIDs, planID) {
		return nil, &PromotionRejectError{Reason: RejectPlanNotEligible}
	}
	if len(durationDaysArr) > 0 && !containsInt(durationDaysArr, durationDays) {
		return nil, &PromotionRejectError{Reason: RejectPlanNotEligible}
	}
	if orderCents < p.MinOrderCents {
		return nil, &PromotionRejectError{Reason: RejectBelowMinimum}
	}
	if p.FirstPurchaseOnly && !isFirstPurchase {
		return nil, &PromotionRejectError{Reason: RejectNotFirstPurchase}
	}
	if len(allowedUserIDs) > 0 && !containsString(allowedUserIDs, userID) {
		return nil, &PromotionRejectError{Reason: RejectUserNotAllowed}
	}

	var userRedemptions int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM (
		   SELECT 1 FROM promotion_redemptions
		   WHERE promotion_id = $1 AND user_id = $2 AND status <> 'released'
		   FOR UPDATE
		 ) locked`,
		p.ID, userID,
	).Scan(&userRedemptions); err != nil {
		return nil, fmt.Errorf("count user redemptions: %w", err)
	}
	if userRedemptions >= p.MaxRedemptionsPerUser {
		return nil, &PromotionRejectError{Reason: RejectUserCapReached}
	}

	tag, err := tx.Exec(ctx,
		`UPDATE promotion_codes
		 SET redeemed_count = redeemed_count + 1
		 WHERE id = $1 AND (max_redemptions IS NULL OR redeemed_count < max_redemptions)`,
		p.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("claim redemption slot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, &PromotionRejectError{Reason: RejectOverallCapReached}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO promotion_redemptions (promotion_id, user_id, order_id, status)
		 VALUES ($1, $2, $3, 'reserved')`,
		p.ID, userID, orderID,
	); err != nil {
		return nil, fmt.Errorf("insert redemption: %w", err)
	}

	discountCents := computeDiscountCents(p.DiscountType, p.DiscountValue, orderCents)

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reserve: %w", err)
	}

	return &PromotionReservation{PromotionID: p.ID, DiscountCents: discountCents}, nil
}

// Release marks orderID's redemption row 'released' and gives the slot back
// to the code's overall cap, guarded so a cancel racing an expiry sweep (or
// either firing twice) cannot double-release: the UPDATE only matches a row
// still 'reserved', and RowsAffected reports whether this call actually was
// the one that transitioned it.
func (s *PromotionService) Release(ctx context.Context, orderID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin release: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var promotionID string
	tag, err := tx.Exec(ctx,
		`UPDATE promotion_redemptions SET status = 'released'
		 WHERE order_id = $1 AND status = 'reserved'`,
		orderID,
	)
	if err != nil {
		return fmt.Errorf("release redemption: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// No reserved redemption for this order - either it never used a
		// code, or this order was already released/confirmed. Both are a
		// no-op, not an error: Cancel and the expiry sweep can both call
		// Release for the same order without coordinating.
		return tx.Commit(ctx)
	}

	if err := tx.QueryRow(ctx,
		`SELECT promotion_id FROM promotion_redemptions WHERE order_id = $1`,
		orderID,
	).Scan(&promotionID); err != nil {
		return fmt.Errorf("load released redemption: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE promotion_codes SET redeemed_count = redeemed_count - 1 WHERE id = $1`,
		promotionID,
	); err != nil {
		return fmt.Errorf("return redemption slot: %w", err)
	}

	return tx.Commit(ctx)
}

// Confirm marks orderID's reservation 'confirmed' once its order is paid,
// closing out the ledger row without changing redeemed_count - the slot was
// already counted as spent the moment Reserve claimed it.
func (s *PromotionService) Confirm(ctx context.Context, orderID string) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE promotion_redemptions SET status = 'confirmed'
		 WHERE order_id = $1 AND status = 'reserved'`,
		orderID,
	); err != nil {
		return fmt.Errorf("confirm redemption: %w", err)
	}
	return nil
}

// computeDiscountCents applies a promotion's discount to orderCents, capped
// so a fixed discount (or, in principle, a rounding edge on a percent one)
// can never take an order negative - it can only reach exactly zero.
func computeDiscountCents(discountType string, discountValue, orderCents int64) int64 {
	var discount int64
	switch discountType {
	case "percent":
		discount = orderCents * discountValue / 100
	case "fixed":
		discount = discountValue
	}
	if discount > orderCents {
		discount = orderCents
	}
	return discount
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func containsInt(list []int, want int) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
