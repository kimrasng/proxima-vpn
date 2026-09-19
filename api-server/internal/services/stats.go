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
}

type Alerts struct {
	Items           []Alert     `json:"items"`
	NodeIssues      []NodeIssue `json:"node_issues"`
	Total           int         `json:"total"`
	PendingRequests int         `json:"pending_requests"`
}

// Thresholds for what counts as a problem worth surfacing. High CPU and memory
// are warnings rather than errors because a node under load is still serving
// traffic; only losing the node outright is an error.
const (
	cpuAlertThreshold    = 80.0
	memoryAlertThreshold = 85.0
	diskAlertThreshold   = 90.0
)

// GetAlerts returns the conditions requiring action. Only non-pending nodes are
// considered: a node mid-registration has never reported metrics, so its zeroed
// readings are absence of data rather than a healthy node.
func (s *StatsService) GetAlerts(ctx context.Context) (Alerts, error) {
	var a Alerts

	rows, err := s.db.Query(ctx, `
		SELECT id::text, name, country, region, status,
		       cpu_usage, memory_usage, disk_usage
		FROM nodes
		WHERE status != 'pending'
		ORDER BY name ASC
	`)
	if err != nil {
		return a, fmt.Errorf("query node alerts: %w", err)
	}
	defer rows.Close()

	var offline, highCPU, highMemory, highDisk int
	a.NodeIssues = []NodeIssue{}

	for rows.Next() {
		var (
			id, name, country, region, status string
			cpu, memory, disk                 float64
		)
		if err := rows.Scan(&id, &name, &country, &region, &status, &cpu, &memory, &disk); err != nil {
			return a, fmt.Errorf("scan node alerts: %w", err)
		}

		issue := NodeIssue{
			NodeID: id, NodeName: name, Country: country, Region: region, Status: status,
		}

		switch {
		case status == "offline":
			offline++
			issue.Kind, issue.Severity, issue.Value = "offline", string(SeverityError), 0
		case cpu >= cpuAlertThreshold:
			highCPU++
			issue.Kind, issue.Severity, issue.Value = "cpu", string(SeverityWarning), cpu
		case memory >= memoryAlertThreshold:
			highMemory++
			issue.Kind, issue.Severity, issue.Value = "memory", string(SeverityWarning), memory
		case disk >= diskAlertThreshold:
			highDisk++
			issue.Kind, issue.Severity, issue.Value = "disk", string(SeverityWarning), disk
		default:
			continue
		}

		a.NodeIssues = append(a.NodeIssues, issue)
	}
	if err := rows.Err(); err != nil {
		return a, fmt.Errorf("scan node alerts: %w", err)
	}

	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM plan_requests WHERE status = 'pending'`,
	).Scan(&a.PendingRequests); err != nil {
		return a, fmt.Errorf("count pending requests: %w", err)
	}

	a.Items = []Alert{}
	for _, candidate := range []Alert{
		{Kind: "node_offline", Severity: string(SeverityError), Count: offline},
		{Kind: "node_cpu", Severity: string(SeverityWarning), Count: highCPU},
		{Kind: "node_memory", Severity: string(SeverityWarning), Count: highMemory},
		{Kind: "node_disk", Severity: string(SeverityWarning), Count: highDisk},
		{Kind: "pending_requests", Severity: string(SeverityInfo), Count: a.PendingRequests},
	} {
		if candidate.Count > 0 {
			a.Items = append(a.Items, candidate)
			a.Total += candidate.Count
		}
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
