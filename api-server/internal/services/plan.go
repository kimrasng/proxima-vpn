package services

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanService centralizes plan-assignment logic that was previously
// duplicated (and had drifted) between the admin plan-request approval flow
// and the Telegram bot's /setplan command - see handlers/admin_plan_request.go
// and telegram/bot.go.
type PlanService struct {
	db *pgxpool.Pool
}

// NewPlanService creates a PlanService.
func NewPlanService(db *pgxpool.Pool) *PlanService {
	return &PlanService{db: db}
}

// AssignPlanToUser assigns planID to userID: sets plan_started_at to now,
// plan_expires_at to now + the plan's duration, resets traffic_used to 0, and
// reactivates the account (status='active', is_active=true). This is the
// single source of truth for "a user was given a plan" - always reset
// traffic and reactivate, regardless of which surface (admin panel, Telegram
// bot) triggered it.
func (s *PlanService) AssignPlanToUser(ctx context.Context, userID, planID string) error {
	var durationDays int
	err := s.db.QueryRow(ctx,
		`SELECT duration_days FROM plans WHERE id = $1`,
		planID,
	).Scan(&durationDays)
	if err != nil {
		return fmt.Errorf("fetch plan: %w", err)
	}

	tag, err := s.db.Exec(ctx,
		`UPDATE users SET plan_id = $1, plan_started_at = NOW(), plan_expires_at = NOW() + make_interval(days => $2),
		 traffic_used = 0, status = 'active', is_active = true
		 WHERE id = $3`,
		planID, durationDays, userID,
	)
	if err != nil {
		return fmt.Errorf("assign plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}

	return nil
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
