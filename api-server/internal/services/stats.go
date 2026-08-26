package services

import (
	"context"
	"fmt"

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
