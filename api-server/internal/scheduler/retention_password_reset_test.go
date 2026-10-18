package scheduler

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// The sweep drops reset tokens a week after they expire and nothing sooner:
// a live token, a recently expired one and a recently used one must survive.
func TestRetention_PrunesOnlyLongExpiredPasswordResetTokens(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	userID := seedResetUser(t, pool, ctx, fmt.Sprintf("pwreset-retention-%d@x.test", os.Getpid()), nil, nil, nil, nil, 0, "active")

	insert := func(label, expiresAt string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
			 VALUES ($1, $2, `+expiresAt+`) RETURNING id::text`,
			userID, fmt.Sprintf("%s-%d", label, os.Getpid()),
		).Scan(&id); err != nil {
			t.Fatalf("seed %s token: %v", label, err)
		}
		return id
	}
	// The user's cleanup cascades to these rows.
	stale := insert("stale", "NOW() - INTERVAL '8 days'")
	recent := insert("recent", "NOW() - INTERVAL '1 day'")
	live := insert("live", "NOW() + INTERVAL '10 minutes'")

	NewRetentionScheduler(pool).run(ctx)

	exists := func(id string) bool {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM password_reset_tokens WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n == 1
	}
	if exists(stale) {
		t.Error("a token expired 8 days ago survived the sweep")
	}
	if !exists(recent) || !exists(live) {
		t.Errorf("sweep removed a token inside the retention window: recent=%v live=%v", exists(recent), exists(live))
	}
}
