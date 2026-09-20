package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Summary holds the user/node counts that were previously computed
// independently (and inconsistently - see below) in three places:
// handlers/admin_stats.go, metrics/metrics.go, and telegram/bot.go's
// handleStats. All three now call GetSummary so the definition of "active
// user" or "counted node" can't drift between the dashboard, Prometheus, and
// the Telegram bot again.
type Summary struct {
	TotalUsers  int64
	ActiveUsers int64
	TotalNodes  int64
	OnlineNodes int64
}

// StatsService computes cross-cutting user/node counts.
type StatsService struct {
	db *pgxpool.Pool
}

// NewStatsService creates a StatsService.
func NewStatsService(db *pgxpool.Pool) *StatsService {
	return &StatsService{db: db}
}

// GetSummary returns the current counts. "Total nodes" excludes nodes still
// in the 'pending' (registered but not yet completed setup) state, matching
// admin_stats.go's pre-existing definition - metrics.go previously counted
// pending nodes too, which this fixes.
func (s *StatsService) GetSummary(ctx context.Context) (Summary, error) {
	var sum Summary

	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&sum.TotalUsers); err != nil {
		return sum, fmt.Errorf("count users: %w", err)
	}
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE status = 'active' AND is_active = true`,
	).Scan(&sum.ActiveUsers); err != nil {
		return sum, fmt.Errorf("count active users: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM nodes WHERE status != 'pending'`).Scan(&sum.TotalNodes); err != nil {
		return sum, fmt.Errorf("count nodes: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM nodes WHERE status = 'online'`).Scan(&sum.OnlineNodes); err != nil {
		return sum, fmt.Errorf("count online nodes: %w", err)
	}

	return sum, nil
}

type NodeTraffic struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Status   string `json:"status"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

type TrafficWindow string

const (
	WindowToday TrafficWindow = "today"
	WindowWeek  TrafficWindow = "week"
	WindowMonth TrafficWindow = "month"
)

// ParseTrafficWindow maps a query parameter to a window, defaulting to today
// for anything unrecognised so a bad parameter cannot become an SQL fragment.
func ParseTrafficWindow(raw string) TrafficWindow {
	switch TrafficWindow(raw) {
	case WindowWeek:
		return WindowWeek
	case WindowMonth:
		return WindowMonth
	default:
		return WindowToday
	}
}

func (w TrafficWindow) since() string {
	switch w {
	case WindowWeek:
		return "CURRENT_DATE - INTERVAL '6 days'"
	case WindowMonth:
		return "DATE_TRUNC('month', CURRENT_DATE)"
	default:
		return "CURRENT_DATE"
	}
}

// GetNodeTraffic includes nodes with no traffic in the window, as zeros, so the
// panel reflects the whole pool rather than only its active part.
func (s *StatsService) GetNodeTraffic(ctx context.Context, w TrafficWindow, limit int) ([]NodeTraffic, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	// The window bound is a fixed expression selected by ParseTrafficWindow,
	// never caller text, so it is safe to interpolate; $1 remains a parameter.
	// ORDER BY repeats the aggregate rather than reusing the select aliases:
	// Postgres only accepts a bare alias there, not one inside an expression.
	query := fmt.Sprintf(`
		SELECT n.id::text, n.name, n.status,
		       COALESCE(SUM(t.up_bytes), 0) AS upload,
		       COALESCE(SUM(t.dn_bytes), 0) AS download
		FROM nodes n
		LEFT JOIN traffic_logs t
		       ON t.node_id = n.id AND t.created_at >= %s
		WHERE n.status != 'pending'
		GROUP BY n.id, n.name, n.status
		ORDER BY COALESCE(SUM(t.up_bytes + t.dn_bytes), 0) DESC, n.name ASC
		LIMIT $1
	`, w.since())

	rows, err := s.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query node traffic: %w", err)
	}
	defer rows.Close()

	result := make([]NodeTraffic, 0, limit)
	for rows.Next() {
		var nt NodeTraffic
		if err := rows.Scan(&nt.NodeID, &nt.NodeName, &nt.Status, &nt.Upload, &nt.Download); err != nil {
			return nil, fmt.Errorf("scan node traffic: %w", err)
		}
		result = append(result, nt)
	}

	return result, rows.Err()
}

// UserNodeTraffic is one node's share of a single user's traffic.
type UserNodeTraffic struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Total    int64  `json:"total"`
}

// GetUserNodeTraffic breaks one user's traffic down by node. Unlike
// GetNodeTraffic it inner-joins, listing only nodes the user actually used:
// padding the pool with zero rows per node would bury the answer in a table
// whose length tracks the node count rather than the user's activity.
//
// The join reaches the user through devices because traffic_logs identifies the
// consumer by device, and the reported bytes are raw - nodes.traffic_multiplier
// applies to the quota charge on users.traffic_used, not to what crossed the
// wire, so applying it here would double-count it.
func (s *StatsService) GetUserNodeTraffic(ctx context.Context, userID string, w TrafficWindow) ([]UserNodeTraffic, error) {
	// The window bound is a fixed expression selected by ParseTrafficWindow,
	// never caller text, so it is safe to interpolate; $1 remains a parameter.
	query := fmt.Sprintf(`
		SELECT n.id::text, n.name,
		       COALESCE(SUM(t.up_bytes), 0) AS upload,
		       COALESCE(SUM(t.dn_bytes), 0) AS download
		FROM traffic_logs t
		JOIN devices d ON t.device_id = d.id
		JOIN nodes n ON t.node_id = n.id
		WHERE d.user_id = $1 AND t.created_at >= %s
		GROUP BY n.id, n.name
		ORDER BY COALESCE(SUM(t.up_bytes + t.dn_bytes), 0) DESC, n.name ASC
	`, w.since())

	rows, err := s.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query user node traffic: %w", err)
	}
	defer rows.Close()

	result := make([]UserNodeTraffic, 0)
	for rows.Next() {
		var ut UserNodeTraffic
		if err := rows.Scan(&ut.NodeID, &ut.NodeName, &ut.Upload, &ut.Download); err != nil {
			return nil, fmt.Errorf("scan user node traffic: %w", err)
		}
		ut.Total = ut.Upload + ut.Download
		result = append(result, ut)
	}

	return result, rows.Err()
}

type Alert struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

type NodeIssue struct {
	NodeID   string  `json:"node_id"`
	NodeName string  `json:"node_name"`
	Country  string  `json:"country"`
	Region   string  `json:"region"`
	Status   string  `json:"status"`
	Kind     string  `json:"kind"`
	Severity string  `json:"severity"`
	Value    float64 `json:"value"`

	// Lifecycle fields, added when alerts became stateful. Additive on purpose:
	// an older client that ignores unknown keys still reads the same response.
	AlertID string `json:"alert_id"`
	State   string `json:"state"`
	// NodeDeleted marks an alert whose node has been removed. Such an alert can
	// never be resolved by evaluation - nothing reports for it - so the UI offers
	// closing it by hand.
	NodeDeleted     bool    `json:"node_deleted"`
	FiredAt         *string `json:"fired_at"`
	DurationSeconds int64   `json:"duration_seconds"`
	Acked           bool    `json:"acked"`
	AckedBy         string  `json:"acked_by"`
	SilencedUntil   *string `json:"silenced_until"`
}

type Alerts struct {
	Items      []Alert     `json:"items"`
	NodeIssues []NodeIssue `json:"node_issues"`
	// Total counts only what an operator still has to look at: firing conditions
	// that are neither acknowledged nor silenced. Stale rows are excluded because
	// their readings are frozen, and pending approvals are excluded because they
	// are a work queue rather than a system condition.
	Total           int    `json:"total"`
	PendingRequests int    `json:"pending_requests"`
	EvaluatedAt     string `json:"evaluated_at"`
}

// GetAlerts reads the persisted alert lifecycle rather than recomputing conditions
// from current node readings. The evaluator in the node monitor owns the
// thresholds and the state machine; this is the read side.
func (s *StatsService) GetAlerts(ctx context.Context) (Alerts, error) {
	a := Alerts{Items: []Alert{}, NodeIssues: []NodeIssue{}}

	open, err := NewAlertService(s.db).ListOpen(ctx)
	if err != nil {
		return a, err
	}

	byKind := map[string]*Alert{}
	var latest time.Time

	for _, o := range open {
		suppressed := o.AckedAt != nil ||
			(o.SilencedUntil != nil && o.SilencedUntil.After(time.Now()))

		issue := NodeIssue{
			NodeID: o.NodeID, NodeName: o.NodeName,
			Country: o.Country, Region: o.Region, Status: o.NodeStatus,
			Kind: o.Kind, Severity: o.Severity, Value: o.Value,
			AlertID: o.ID, State: o.State, NodeDeleted: o.NodeDeleted,
			Acked: o.AckedAt != nil, AckedBy: o.AckedBy,
		}
		if o.FiredAt != nil {
			stamp := o.FiredAt.Format(time.RFC3339)
			issue.FiredAt = &stamp
			issue.DurationSeconds = int64(time.Since(*o.FiredAt).Seconds())
		}
		if o.SilencedUntil != nil {
			stamp := o.SilencedUntil.Format(time.RFC3339)
			issue.SilencedUntil = &stamp
		}
		a.NodeIssues = append(a.NodeIssues, issue)

		if o.EvaluatedAt.After(latest) {
			latest = o.EvaluatedAt
		}

		if o.State != AlertStateFiring || suppressed {
			continue
		}
		key := "node_" + o.Kind
		if existing, ok := byKind[key]; ok {
			existing.Count++
		} else {
			byKind[key] = &Alert{Kind: key, Severity: o.Severity, Count: 1}
		}
		a.Total++
	}

	// Stable order so the dashboard panel does not reshuffle between polls.
	for _, kind := range []string{
		"node_offline", "node_xray_down", "node_shaping_failed",
		"node_cpu", "node_memory", "node_disk",
	} {
		if item, ok := byKind[kind]; ok {
			a.Items = append(a.Items, *item)
		}
	}

	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM plan_requests WHERE status = 'pending'`,
	).Scan(&a.PendingRequests); err != nil {
		return a, fmt.Errorf("count pending requests: %w", err)
	}

	if !latest.IsZero() {
		a.EvaluatedAt = latest.Format(time.RFC3339)
	}

	return a, nil
}

type SnapshotValues struct {
	ActiveAlerts int   `json:"active_alerts"`
	OnlineNodes  int   `json:"online_nodes"`
	TotalNodes   int   `json:"total_nodes"`
	OnlineUsers  int   `json:"online_users"`
	TotalUsers   int   `json:"total_users"`
	TrafficToday int64 `json:"traffic_today"`
}

func (s *StatsService) RecordSnapshot(ctx context.Context, v SnapshotValues) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO dashboard_snapshots
			(active_alerts, online_nodes, total_nodes, online_users, total_users, traffic_today)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, v.ActiveAlerts, v.OnlineNodes, v.TotalNodes, v.OnlineUsers, v.TotalUsers, v.TrafficToday)
	if err != nil {
		return fmt.Errorf("record snapshot: %w", err)
	}
	return nil
}

// GetBaseline returns the newest snapshot at least `age` old, which is what the
// current figures are compared against. Reported as absent rather than zero
// when no such row exists: a delta of "+3" against a baseline that was never
// recorded would read as growth when it is really a first measurement.
func (s *StatsService) GetBaseline(ctx context.Context, age time.Duration) (SnapshotValues, bool, error) {
	var v SnapshotValues

	err := s.db.QueryRow(ctx, `
		SELECT active_alerts, online_nodes, total_nodes, online_users, total_users, traffic_today
		FROM dashboard_snapshots
		WHERE recorded_at <= NOW() - $1::interval
		ORDER BY recorded_at DESC
		LIMIT 1
	`, age.String()).Scan(
		&v.ActiveAlerts, &v.OnlineNodes, &v.TotalNodes,
		&v.OnlineUsers, &v.TotalUsers, &v.TrafficToday,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	if err != nil {
		return v, false, fmt.Errorf("query baseline snapshot: %w", err)
	}

	return v, true, nil
}
