package handlers

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// resolveSessionExpiry returns the JWT expiry duration to use for a login:
// the admin Settings table's 'session_timeout' (seconds, see
// web/src/pages/admin/Settings.tsx) if set and valid, otherwise fallback
// (the config.yaml jwt.admin_expiry/user_expiry duration the handler was
// constructed with). Previously session_timeout was saved by the Settings
// page but never read anywhere, so it had no effect on issued tokens.
func resolveSessionExpiry(ctx context.Context, db *pgxpool.Pool, fallback time.Duration) time.Duration {
	var value string
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'session_timeout'`).Scan(&value); err != nil {
		return fallback
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
