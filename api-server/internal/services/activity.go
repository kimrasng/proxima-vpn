package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Severity classifies an activity entry for display. It is deliberately
// independent of log levels: an offline node is a warning to an operator even
// though the API served the request successfully.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
	SeveritySuccess Severity = "success"
)

// Event names are the stable contract between the recording call sites and the
// web UI, which maps them to translated sentences. Renaming one silently turns
// its feed rows into untranslated text, so they are enumerated here rather than
// written as literals at each call site.
const (
	EventUserLogin      = "user.login"
	EventUserRegistered = "user.registered"
	EventUserCreated    = "user.created"
	EventUserDeleted    = "user.deleted"
	EventAdminLogin     = "admin.login"
	EventNodeOffline    = "node.offline"
	EventNodeRegistered = "node.registered"
	EventPlanRequested  = "plan.requested"
	EventPlanApproved   = "plan.approved"
	EventPlanRejected   = "plan.rejected"

	EventSessionTerminated = "session.terminated"
)

// Entry is one row of the activity feed.
type Entry struct {
	ID         string         `json:"id"`
	EventType  string         `json:"event_type"`
	Severity   string         `json:"severity"`
	ActorType  string         `json:"actor_type"`
	ActorID    string         `json:"actor_id"`
	ActorLabel string         `json:"actor_label"`
	TargetType *string        `json:"target_type"`
	TargetID   *string        `json:"target_id"`
	Detail     map[string]any `json:"detail"`
	CreatedAt  string         `json:"created_at"`
}

// Record describes an event to append to the feed.
type Record struct {
	EventType  string
	Severity   Severity
	ActorType  string
	ActorID    string
	ActorLabel string
	TargetType string
	TargetID   string
	Detail     map[string]any
}

// ActivityService appends to and reads the activity feed.
type ActivityService struct {
	db *pgxpool.Pool
}

// NewActivityService creates an ActivityService.
func NewActivityService(db *pgxpool.Pool) *ActivityService {
	return &ActivityService{db: db}
}

// Log appends an entry. Failures are logged and swallowed: the feed is an
// observability aid, so a full disk or a lock on activity_logs must not turn a
// successful login or plan approval into an error for the caller.
func (s *ActivityService) Log(ctx context.Context, r Record) {
	if s == nil || s.db == nil {
		return
	}

	severity := r.Severity
	if severity == "" {
		severity = SeverityInfo
	}
	actorType := r.ActorType
	if actorType == "" {
		actorType = "system"
	}

	detail := r.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		log.Printf("[Activity] dropping %s: encoding detail: %v", r.EventType, err)
		return
	}

	var targetType, targetID *string
	if r.TargetType != "" {
		targetType = &r.TargetType
	}
	if r.TargetID != "" {
		targetID = &r.TargetID
	}

	if _, err := s.db.Exec(ctx, `
		INSERT INTO activity_logs
			(event_type, severity, actor_type, actor_id, actor_label, target_type, target_id, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, r.EventType, string(severity), actorType, r.ActorID, r.ActorLabel,
		targetType, targetID, encoded,
	); err != nil {
		log.Printf("[Activity] dropping %s: %v", r.EventType, err)
	}
}

// List returns the most recent entries, newest first.
func (s *ActivityService) List(ctx context.Context, limit int) ([]Entry, error) {
	return s.list(ctx, limit, "", "")
}

// ListForTarget returns the most recent entries recorded against one target,
// newest first. An empty targetType or targetID widens to the unfiltered feed
// rather than matching nothing, so a caller that omits the filter keeps the
// whole-panel behaviour instead of silently rendering an empty history.
func (s *ActivityService) ListForTarget(ctx context.Context, limit int, targetType, targetID string) ([]Entry, error) {
	return s.list(ctx, limit, targetType, targetID)
}

func (s *ActivityService) list(ctx context.Context, limit int, targetType, targetID string) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}

	filtered := targetType != "" && targetID != ""

	query := `
		SELECT id::text, event_type, severity, actor_type, actor_id, actor_label,
		       target_type, target_id, detail, created_at
		FROM activity_logs
		WHERE ($2::text IS NULL OR (target_type = $2 AND target_id = $3))
		ORDER BY created_at DESC
		LIMIT $1
	`
	var filterType, filterID *string
	if filtered {
		filterType = &targetType
		filterID = &targetID
	}

	rows, err := s.db.Query(ctx, query, limit, filterType, filterID)
	if err != nil {
		return nil, fmt.Errorf("query activity: %w", err)
	}
	defer rows.Close()

	entries := make([]Entry, 0, limit)
	for rows.Next() {
		var e Entry
		var detail []byte
		var createdAt time.Time
		if err := rows.Scan(
			&e.ID, &e.EventType, &e.Severity, &e.ActorType, &e.ActorID, &e.ActorLabel,
			&e.TargetType, &e.TargetID, &detail, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		e.Detail = map[string]any{}
		if len(detail) > 0 {
			_ = json.Unmarshal(detail, &e.Detail)
		}
		e.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		entries = append(entries, e)
	}

	return entries, rows.Err()
}
