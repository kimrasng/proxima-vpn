package services

import (
	"context"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Actor types distinguish a panel admin's attempt from a subscriber's. Both land
// in one table because "who tried to get in, from where" is a single question.
const (
	LoginActorUser  = "user"
	LoginActorAdmin = "admin"
)

// Failure reasons are a closed set, never request text. The login endpoint is
// reachable without credentials, so anything derived from the request body would
// let an anonymous caller write content of their choosing into the table.
const (
	LoginFailureUnknownEmail = "unknown_email"
	LoginFailureBadPassword  = "bad_password"
	LoginFailureSuspended    = "suspended"
	LoginFailureBadTOTP      = "bad_totp"
)

// LoginAttempt describes one authentication attempt to persist.
type LoginAttempt struct {
	UserID    string
	ActorType string
	Email     string
	Success   bool
	Reason    string
	IP        string
	UserAgent string
}

// LoginEntry is one row of a user's login history.
type LoginEntry struct {
	ID            string  `json:"id"`
	UserID        *string `json:"user_id"`
	ActorType     string  `json:"actor_type"`
	Email         string  `json:"attempted_email"`
	Success       bool    `json:"success"`
	FailureReason string  `json:"failure_reason"`
	IP            string  `json:"ip"`
	UserAgent     string  `json:"user_agent"`
	CreatedAt     string  `json:"created_at"`
}

// LoginHistoryService appends to and reads the login attempt log.
type LoginHistoryService struct {
	db *pgxpool.Pool
}

// NewLoginHistoryService creates a LoginHistoryService.
func NewLoginHistoryService(db *pgxpool.Pool) *LoginHistoryService {
	return &LoginHistoryService{db: db}
}

// Record appends one attempt. Failures are logged and swallowed for the same
// reason as the activity feed: an audit write must not turn a valid login into
// an error, nor turn a rejected login into a 500 that reveals the write failed.
//
// Both caller-supplied strings are truncated on a rune boundary. A plain byte
// slice would cut a multi-byte character in half, and Postgres rejects invalid
// UTF-8 for the whole INSERT - which let a client suppress its own audit row by
// sending a long non-ASCII User-Agent.
func (s *LoginHistoryService) Record(ctx context.Context, a LoginAttempt) {
	if s == nil || s.db == nil {
		return
	}

	actorType := a.ActorType
	if actorType == "" {
		actorType = LoginActorUser
	}

	var userID *string
	if a.UserID != "" {
		userID = &a.UserID
	}

	if _, err := s.db.Exec(ctx, `
		INSERT INTO login_history
			(user_id, actor_type, attempted_email, success, failure_reason, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, userID, actorType, truncateUTF8(a.Email, maxEmailBytes), a.Success, a.Reason,
		a.IP, truncateUTF8(a.UserAgent, maxUserAgentBytes),
	); err != nil {
		log.Printf("[LoginHistory] dropping attempt for %q: %v", a.Email, err)
	}
}

const (
	maxEmailBytes     = 320
	maxUserAgentBytes = 512
)

// truncateUTF8 cuts s to at most limit bytes without splitting a rune.
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// ListForUser returns one user's attempts, newest first.
func (s *LoginHistoryService) ListForUser(ctx context.Context, userID string, limit int) ([]LoginEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.db.Query(ctx, `
		SELECT id::text, user_id::text, actor_type, attempted_email, success,
		       failure_reason, ip, user_agent, created_at
		FROM login_history
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query login history: %w", err)
	}
	defer rows.Close()

	entries := make([]LoginEntry, 0, limit)
	for rows.Next() {
		var e LoginEntry
		var createdAt time.Time
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.ActorType, &e.Email, &e.Success,
			&e.FailureReason, &e.IP, &e.UserAgent, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan login history: %w", err)
		}
		e.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		entries = append(entries, e)
	}

	return entries, rows.Err()
}
