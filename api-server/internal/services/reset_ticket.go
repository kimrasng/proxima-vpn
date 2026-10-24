package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reset ticket targets an administrator can grant to.
const (
	ResetTargetUser = "user"
	ResetTargetPlan = "plan"
	ResetTargetAll  = "all"
)

var (
	ErrResetTicketNotFound = errors.New("reset ticket not found")
	ErrResetTicketUsed     = errors.New("reset ticket already used")
	ErrResetTicketExpired  = errors.New("reset ticket expired")
	// ErrResetNotApplicable: the account has no running plan with a data cap,
	// so spending a ticket would reset nothing the user can feel.
	ErrResetNotApplicable = errors.New("no capped plan to reset")
	ErrResetNoRecipients  = errors.New("no recipients for this grant")
	ErrResetInvalidGrant  = errors.New("invalid reset ticket grant")
)

// UserResetTicket is one ticket as an administrator sees it on the user's page:
// spent and lapsed tickets stay listed so the history explains past resets.
type UserResetTicket struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	GrantedAt  time.Time  `json:"granted_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	UsedAt     *time.Time `json:"used_at"`
	ResetBytes *int64     `json:"reset_bytes"`
}

// ResetTicketGrant is one administrator grant as the admin history shows it.
type ResetTicketGrant struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	TargetType     string    `json:"target_type"`
	TargetUserID   *string   `json:"target_user_id"`
	TargetEmail    *string   `json:"target_email"`
	TargetPlanID   *string   `json:"target_plan_id"`
	TargetPlanName *string   `json:"target_plan_name"`
	ExpiresAt      time.Time `json:"expires_at"`
	GrantedCount   int       `json:"granted_count"`
	UsedCount      int       `json:"used_count"`
	GrantedBy      string    `json:"granted_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// ResetTicket is a ticket the user still holds.
type ResetTicket struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GrantRequest describes who receives a ticket and until when it is valid.
type GrantRequest struct {
	Name       string
	TargetType string
	UserID     string
	PlanID     string
	ExpiresAt  time.Time
	ActorID    string
	ActorLabel string
}

// RedeemResult is what a successful redemption changed.
type RedeemResult struct {
	TicketName string
	ResetBytes int64
}

// ResetTicketService issues and redeems data reset tickets.
type ResetTicketService struct {
	db  *pgxpool.Pool
	act *ActivityService
}

// NewResetTicketService creates a ResetTicketService.
func NewResetTicketService(db *pgxpool.Pool) *ResetTicketService {
	return &ResetTicketService{db: db, act: NewActivityService(db)}
}

// Grant issues one ticket to every recipient the request names, in a single
// transaction, and returns the grant row. A plan grant reaches only accounts
// whose term on that plan is still running; "all" reaches every account that
// has not been deactivated. A grant that would reach nobody is rejected so the
// history never fills with empty entries.
func (s *ResetTicketService) Grant(ctx context.Context, req GrantRequest) (*ResetTicketGrant, error) {
	if req.Name == "" || !req.ExpiresAt.After(time.Now()) {
		return nil, ErrResetInvalidGrant
	}
	var userID, planID *string
	var recipients string
	var args []any
	switch req.TargetType {
	case ResetTargetUser:
		if req.UserID == "" {
			return nil, ErrResetInvalidGrant
		}
		userID = &req.UserID
		recipients = `SELECT id FROM users WHERE id = $1::uuid AND is_active = true`
		args = []any{req.UserID}
	case ResetTargetPlan:
		if req.PlanID == "" {
			return nil, ErrResetInvalidGrant
		}
		planID = &req.PlanID
		recipients = `SELECT id FROM users WHERE plan_id = $1::uuid AND is_active = true
		              AND plan_expires_at IS NOT NULL AND plan_expires_at > NOW()`
		args = []any{req.PlanID}
	case ResetTargetAll:
		// Every target passes exactly one argument as $1, so the shared INSERT
		// below can number its own arguments from $2.
		recipients = `SELECT id FROM users WHERE is_active = true AND $1::text = ''`
		args = []any{""}
	default:
		return nil, ErrResetInvalidGrant
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Counting first keeps an unknown or deactivated target from reaching the
	// grant row's foreign keys, so it surfaces as "nobody to grant to".
	var recipientCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM (`+recipients+`) r`, args...).Scan(&recipientCount); err != nil {
		return nil, fmt.Errorf("count recipients: %w", err)
	}
	if recipientCount == 0 {
		return nil, ErrResetNoRecipients
	}

	g := &ResetTicketGrant{Name: req.Name, TargetType: req.TargetType, TargetUserID: userID, TargetPlanID: planID, ExpiresAt: req.ExpiresAt, GrantedBy: req.ActorLabel}
	if err := tx.QueryRow(ctx,
		`INSERT INTO reset_ticket_grants (name, target_type, target_user_id, target_plan_id, expires_at, granted_by_id, granted_by_label)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id::text, created_at`,
		req.Name, req.TargetType, userID, planID, req.ExpiresAt, req.ActorID, req.ActorLabel,
	).Scan(&g.ID, &g.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert grant: %w", err)
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO reset_tickets (grant_id, user_id, name, expires_at)
		 SELECT $2::uuid, r.id, $3, $4 FROM (`+recipients+`) r`,
		append(args, g.ID, req.Name, req.ExpiresAt)...,
	)
	if err != nil {
		return nil, fmt.Errorf("insert tickets: %w", err)
	}
	g.GrantedCount = int(tag.RowsAffected())
	if g.GrantedCount == 0 {
		return nil, ErrResetNoRecipients
	}
	if _, err := tx.Exec(ctx, `UPDATE reset_ticket_grants SET granted_count = $2 WHERE id = $1`, g.ID, g.GrantedCount); err != nil {
		return nil, fmt.Errorf("record grant count: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit grant: %w", err)
	}

	detail := map[string]any{"name": req.Name, "target_type": req.TargetType, "granted_count": g.GrantedCount, "expires_at": req.ExpiresAt}
	targetType, targetID := "reset_ticket_grant", g.ID
	if userID != nil {
		targetType, targetID = "user", *userID
	}
	s.act.Log(ctx, Record{
		EventType: EventResetTicketGranted, Severity: SeverityInfo,
		ActorType: "admin", ActorID: req.ActorID, ActorLabel: req.ActorLabel,
		TargetType: targetType, TargetID: targetID, Detail: detail,
	})
	return g, nil
}

// ListGrants returns the most recent grants, newest first.
func (s *ResetTicketService) ListGrants(ctx context.Context, limit int) ([]ResetTicketGrant, error) {
	rows, err := s.db.Query(ctx,
		`SELECT g.id::text, g.name, g.target_type, g.target_user_id::text, u.email,
		        g.target_plan_id::text, p.name, g.expires_at, g.granted_count,
		        (SELECT COUNT(*) FROM reset_tickets t WHERE t.grant_id = g.id AND t.used_at IS NOT NULL)::int,
		        g.granted_by_label, g.created_at
		 FROM reset_ticket_grants g
		 LEFT JOIN users u ON u.id = g.target_user_id
		 LEFT JOIN plans p ON p.id = g.target_plan_id
		 ORDER BY g.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()
	out := make([]ResetTicketGrant, 0)
	for rows.Next() {
		var g ResetTicketGrant
		if err := rows.Scan(&g.ID, &g.Name, &g.TargetType, &g.TargetUserID, &g.TargetEmail,
			&g.TargetPlanID, &g.TargetPlanName, &g.ExpiresAt, &g.GrantedCount, &g.UsedCount,
			&g.GrantedBy, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListUsable returns the tickets userID can still spend, soonest-expiring first.
func (s *ResetTicketService) ListUsable(ctx context.Context, userID string) ([]ResetTicket, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id::text, name, granted_at, expires_at FROM reset_tickets
		 WHERE user_id = $1 AND used_at IS NULL AND expires_at > NOW()
		 ORDER BY expires_at, granted_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()
	out := make([]ResetTicket, 0)
	for rows.Next() {
		var t ResetTicket
		if err := rows.Scan(&t.ID, &t.Name, &t.GrantedAt, &t.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan ticket: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Redeem spends ticketID and zeroes the user's data usage for the current
// period. The user row is locked first, so two redemptions by the same user
// (the same ticket twice, or two tickets at once) run one after the other and
// the ticket's own "unused and unexpired" condition decides each. The monthly
// anchor (plan_started_at) is untouched, so the next scheduled reset still
// lands on the usual day; traffic_reset_at = NOW() only keeps the scheduler
// from firing a second reset inside the period this ticket just reset.
func (s *ResetTicketService) Redeem(ctx context.Context, userID, ticketID, actorLabel string) (*RedeemResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin redeem: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var trafficUsed int64
	var capped bool
	err = tx.QueryRow(ctx,
		`SELECT u.traffic_used,
		        (u.plan_id IS NOT NULL AND u.plan_expires_at > NOW() AND COALESCE(p.traffic_limit, 0) > 0)
		 FROM users u LEFT JOIN plans p ON p.id = u.plan_id
		 WHERE u.id = $1 FOR UPDATE OF u`, userID,
	).Scan(&trafficUsed, &capped)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResetTicketNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock user: %w", err)
	}

	var name string
	var usedAt *time.Time
	var expiresAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT name, used_at, expires_at FROM reset_tickets WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		ticketID, userID,
	).Scan(&name, &usedAt, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResetTicketNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock ticket: %w", err)
	}
	switch {
	case usedAt != nil:
		return nil, ErrResetTicketUsed
	case !expiresAt.After(time.Now()):
		return nil, ErrResetTicketExpired
	case !capped:
		return nil, ErrResetNotApplicable
	}

	if _, err := tx.Exec(ctx,
		`UPDATE reset_tickets SET used_at = NOW(), reset_bytes = $2 WHERE id = $1`, ticketID, trafficUsed,
	); err != nil {
		return nil, fmt.Errorf("spend ticket: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET traffic_used = 0, traffic_reset_at = NOW(), updated_at = NOW() WHERE id = $1`, userID,
	); err != nil {
		return nil, fmt.Errorf("reset traffic: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit redeem: %w", err)
	}

	s.act.Log(ctx, Record{
		EventType: EventResetTicketUsed, Severity: SeverityInfo,
		ActorType: "user", ActorID: userID, ActorLabel: actorLabel,
		TargetType: "user", TargetID: userID,
		Detail: map[string]any{"ticket_id": ticketID, "name": name, "reset_bytes": trafficUsed},
	})
	return &RedeemResult{TicketName: name, ResetBytes: trafficUsed}, nil
}

// ListForUser returns every ticket userID was ever granted, newest first.
func (s *ResetTicketService) ListForUser(ctx context.Context, userID string) ([]UserResetTicket, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id::text, name, granted_at, expires_at, used_at, reset_bytes
		 FROM reset_tickets WHERE user_id = $1 ORDER BY granted_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user tickets: %w", err)
	}
	defer rows.Close()
	out := make([]UserResetTicket, 0)
	for rows.Next() {
		var t UserResetTicket
		if err := rows.Scan(&t.ID, &t.Name, &t.GrantedAt, &t.ExpiresAt, &t.UsedAt, &t.ResetBytes); err != nil {
			return nil, fmt.Errorf("scan user ticket: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke deletes one unused ticket of userID. A spent ticket is the record of
// a reset that already happened, so it is kept; revoking it would rewrite
// history without giving the data back.
func (s *ResetTicketService) Revoke(ctx context.Context, userID, ticketID, actorID, actorLabel string) error {
	var name string
	err := s.db.QueryRow(ctx,
		`DELETE FROM reset_tickets WHERE id = $1 AND user_id = $2 AND used_at IS NULL RETURNING name`,
		ticketID, userID,
	).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		var used bool
		if qerr := s.db.QueryRow(ctx,
			`SELECT used_at IS NOT NULL FROM reset_tickets WHERE id = $1 AND user_id = $2`, ticketID, userID,
		).Scan(&used); qerr == nil && used {
			return ErrResetTicketUsed
		}
		return ErrResetTicketNotFound
	}
	if err != nil {
		return fmt.Errorf("revoke ticket: %w", err)
	}
	s.act.Log(ctx, Record{
		EventType: EventResetTicketRevoked, Severity: SeverityInfo,
		ActorType: "admin", ActorID: actorID, ActorLabel: actorLabel,
		TargetType: "user", TargetID: userID,
		Detail: map[string]any{"ticket_id": ticketID, "name": name},
	})
	return nil
}

// ListUsedForUser returns the tickets userID has spent, newest first.
func (s *ResetTicketService) ListUsedForUser(ctx context.Context, userID string, limit int) ([]UserResetTicket, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id::text, name, granted_at, expires_at, used_at, reset_bytes
		 FROM reset_tickets WHERE user_id = $1 AND used_at IS NOT NULL
		 ORDER BY used_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list used tickets: %w", err)
	}
	defer rows.Close()
	out := make([]UserResetTicket, 0)
	for rows.Next() {
		var t UserResetTicket
		if err := rows.Scan(&t.ID, &t.Name, &t.GrantedAt, &t.ExpiresAt, &t.UsedAt, &t.ResetBytes); err != nil {
			return nil, fmt.Errorf("scan used ticket: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
