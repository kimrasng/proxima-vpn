package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanService centralizes plan-assignment logic shared by the checkout order
// flow and the Telegram bot's /setplan command - see services/payment.go and
// telegram/bot.go.
type PlanService struct {
	db *pgxpool.Pool
}

// NewPlanService creates a PlanService.
func NewPlanService(db *pgxpool.Pool) *PlanService {
	return &PlanService{db: db}
}

// GrantKind classifies which of the three grant cases GrantPlan took, purely
// as a report to the caller (logging, tests) - it does not feed back into the
// SQL, which is derived independently inside the same transaction.
type GrantKind string

const (
	// GrantRenewal: same plan, current term unexpired - term extended,
	// traffic usage and the reset anchor preserved.
	GrantRenewal GrantKind = "renewal"
	// GrantChange: different plan, current term unexpired - fresh term,
	// usage cleared, but the unexpired remainder is carried into the new term.
	GrantChange GrantKind = "change"
	// GrantFresh: no prior term, or the prior term already expired - fresh
	// term with no remainder to carry.
	GrantFresh GrantKind = "fresh"
)

// GrantResult reports the row state GrantPlan produced and which case fired.
type GrantResult struct {
	Kind    GrantKind
	Carried time.Duration
	PlanID  string
	// PlanExpiresBefore is plan_expires_at as it stood inside the grant's own
	// locked read, before the term was extended. nil means the user had no
	// prior term at all - distinct from a non-nil past timestamp, which is an
	// expired one.
	PlanExpiresBefore *time.Time
	PlanStartedAt     time.Time
	PlanExpiresAt     time.Time
	TrafficUsed       int64
	TrafficResetAt    *time.Time
	Status            string
}

// GrantPlan is the single entry point for putting a user on a plan, covering
// three cases without the caller having to distinguish them:
//
//   - Renewing the same plan before the current term expires extends
//     plan_expires_at from the existing value and leaves traffic_used,
//     traffic_reset_at and plan_started_at untouched, so the monthly reset
//     anchor does not move.
//   - Changing to a different plan, or renewing after expiry, starts a fresh
//     term (plan_started_at = NOW()) and clears traffic_used. A change made
//     before the old term expired still carries its unexpired remainder into
//     the new plan_expires_at, so switching plans never shortens paid time.
//   - Both cases compute plan_expires_at with the same expression -
//     GREATEST(old_expires_at, NOW()) + new plan's duration - because the
//     "carried remainder" in the change case and "no remainder" in the
//     expired/fresh case are the same formula once expired collapses the
//     GREATEST to NOW().
//
// A renewal that reactivates a user still over their (unreset) traffic cap
// would immediately be re-suspended by ExpiryCheckScheduler; status is set to
// 'suspended' rather than 'active' in that case to skip the flap.
func (s *PlanService) GrantPlan(ctx context.Context, userID, planID string) (*GrantResult, error) {
	return s.grantPlan(ctx, userID, planID, nil)
}

// GrantPlanWithDuration grants planID for an explicitly purchased term,
// overriding the plan's duration_days with durationDays - used by the order
// checkout flow, where a plan can be bought at any of several durations and
// duration_days alone no longer says which one this grant is for.
func (s *PlanService) GrantPlanWithDuration(ctx context.Context, userID, planID string, durationDays int) (*GrantResult, error) {
	return s.grantPlan(ctx, userID, planID, &durationDays)
}

// grantPlan is GrantPlan's body. durationDays == nil reproduces the plan's own
// duration_days (the legacy/default grant length); a non-nil value overrides
// it for a specific purchased term without touching the renew/change/fresh
// classification or the traffic/status side effects above.
func (s *PlanService) grantPlan(ctx context.Context, userID, planID string, durationDays *int) (*GrantResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR UPDATE locks the row before classifying it, so a concurrent grant on
	// the same user cannot be classified against a row this transaction is
	// about to change out from under it. The same locked read is what yields
	// the pre-grant expiry, so the reported before value cannot disagree with
	// the row the UPDATE below extends.
	var isRenewal bool
	var carriedSeconds int64
	var expiresBefore *time.Time
	err = tx.QueryRow(ctx,
		`SELECT (plan_id IS NOT NULL AND plan_id = $2
		         AND plan_expires_at IS NOT NULL AND plan_expires_at > NOW()) AS is_renewal,
		        EXTRACT(EPOCH FROM
		          GREATEST(COALESCE(plan_expires_at, NOW()), NOW()) - NOW()
		        )::bigint AS carried_seconds,
		        plan_expires_at
		 FROM users WHERE id = $1 FOR UPDATE`,
		userID, planID,
	).Scan(&isRenewal, &carriedSeconds, &expiresBefore)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("classify grant: %w", err)
	}

	r := &GrantResult{PlanID: planID, PlanExpiresBefore: expiresBefore}
	err = tx.QueryRow(ctx,
		`UPDATE users u
		 SET plan_id          = p.id,
		     plan_started_at  = CASE WHEN $3::bool THEN u.plan_started_at ELSE NOW() END,
		     plan_expires_at  = GREATEST(COALESCE(u.plan_expires_at, NOW()), NOW())
		                        + make_interval(days => COALESCE($4::int, p.duration_days)),
		     traffic_used     = CASE WHEN $3::bool THEN u.traffic_used ELSE 0 END,
		     traffic_reset_at = CASE WHEN $3::bool THEN u.traffic_reset_at ELSE NOW() END,
		     status           = CASE WHEN $3::bool
		                              AND COALESCE(p.traffic_limit, 0) > 0
		                              AND u.traffic_used >= p.traffic_limit
		                         THEN 'suspended' ELSE 'active' END,
		     is_active        = true,
		     updated_at       = NOW()
		 FROM plans p
		 WHERE u.id = $1 AND p.id = $2
		 RETURNING u.plan_started_at, u.plan_expires_at, u.traffic_used, u.traffic_reset_at, u.status`,
		userID, planID, isRenewal, durationDays,
	).Scan(&r.PlanStartedAt, &r.PlanExpiresAt, &r.TrafficUsed, &r.TrafficResetAt, &r.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("plan not found")
		}
		return nil, fmt.Errorf("grant plan: %w", err)
	}

	switch {
	case isRenewal:
		r.Kind = GrantRenewal
	case carriedSeconds > 0:
		r.Kind = GrantChange
		r.Carried = time.Duration(carriedSeconds) * time.Second
	default:
		r.Kind = GrantFresh
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit grant: %w", err)
	}
	return r, nil
}

// AssignPlanToUser assigns planID to userID through GrantPlan. Kept as a
// same-signature wrapper for its existing caller (the Telegram bot's
// /setplan): it still just gets "the user is now on this plan" without
// inspecting which of GrantPlan's three cases fired.
func (s *PlanService) AssignPlanToUser(ctx context.Context, userID, planID string) error {
	_, err := s.GrantPlan(ctx, userID, planID)
	return err
}

// AssignPlanByName looks up a plan by name (must be active) and assigns it to
// the user identified by email, returning the plan's duration in days. Used
// by the Telegram bot's /setplan command, which addresses users/plans by
// name rather than ID.
func (s *PlanService) AssignPlanByName(ctx context.Context, userEmail, planName string) (durationDays int, err error) {
	var userID, planID string
	err = s.db.QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1`, userEmail,
	).Scan(&userID)
	if err != nil {
		return 0, fmt.Errorf("user not found")
	}

	err = s.db.QueryRow(ctx,
		`SELECT id, duration_days FROM plans WHERE name = $1 AND is_active = true`, planName,
	).Scan(&planID, &durationDays)
	if err != nil {
		return 0, fmt.Errorf("plan not found or inactive")
	}

	if err := s.AssignPlanToUser(ctx, userID, planID); err != nil {
		return 0, err
	}
	return durationDays, nil
}
