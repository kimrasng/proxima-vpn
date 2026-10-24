package services_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func resetTicketFixture(t *testing.T) (*pgxpool.Pool, context.Context, *services.ResetTicketService, string, func(string, int64) string) {
	t.Helper()
	pool := testDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("reset-%d", time.Now().UnixNano())
	limit := int64(1000)
	planID := seedPlan(t, pool, ctx, suffix, 30, &limit)
	n := 0
	user := func(status string, used int64) string {
		n++
		start := time.Now().Add(-5 * 24 * time.Hour)
		expires := time.Now().Add(25 * 24 * time.Hour)
		id := seedUser(t, pool, ctx, fmt.Sprintf("%s-%d@x.test", suffix, n), planID, &start, &expires, nil, used, status)
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reset_tickets WHERE user_id = $1`, id) })
		return id
	}
	// Registered before the users are seeded, so it runs after their cleanups
	// and also removes user-target grants whose target was nulled on delete.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM reset_ticket_grants WHERE target_plan_id = $1 OR name LIKE $2
			    OR (target_type = 'user' AND target_user_id IS NULL AND name = 'test ticket')`, planID, suffix+"%")
	})
	return pool, ctx, services.NewResetTicketService(pool), planID, user
}

func grantOne(t *testing.T, ctx context.Context, svc *services.ResetTicketService, userID string, expires time.Time) string {
	t.Helper()
	if _, err := svc.Grant(ctx, services.GrantRequest{Name: "test ticket", TargetType: services.ResetTargetUser, UserID: userID, ExpiresAt: expires, ActorLabel: "admin@x.test"}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	tickets, err := svc.ListUsable(ctx, userID)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("list usable: %v (%d tickets)", err, len(tickets))
	}
	return tickets[0].ID
}

func TestResetTicket_RedeemZeroesUsageOnce(t *testing.T) {
	pool, ctx, svc, _, user := resetTicketFixture(t)
	userID := user("active", 1500)
	ticketID := grantOne(t, ctx, svc, userID, time.Now().Add(7*24*time.Hour))

	res, err := svc.Redeem(ctx, userID, ticketID, "u@x.test")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if res.ResetBytes != 1500 {
		t.Errorf("ResetBytes = %d, want 1500", res.ResetBytes)
	}
	var used int64
	var resetAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT traffic_used, traffic_reset_at FROM users WHERE id = $1`, userID).Scan(&used, &resetAt); err != nil {
		t.Fatal(err)
	}
	if used != 0 || resetAt == nil {
		t.Errorf("after redeem traffic_used=%d reset_at=%v, want 0 and set", used, resetAt)
	}
	if _, err := svc.Redeem(ctx, userID, ticketID, "u@x.test"); !errors.Is(err, services.ErrResetTicketUsed) {
		t.Errorf("second redeem err = %v, want ErrResetTicketUsed", err)
	}
	if left, _ := svc.ListUsable(ctx, userID); len(left) != 0 {
		t.Errorf("used ticket still listed: %d", len(left))
	}
}

func TestResetTicket_RejectsExpiredAndForeignTickets(t *testing.T) {
	pool, ctx, svc, _, user := resetTicketFixture(t)
	owner := user("active", 500)
	other := user("active", 700)
	ticketID := grantOne(t, ctx, svc, owner, time.Now().Add(time.Hour))

	if _, err := svc.Redeem(ctx, other, ticketID, "other@x.test"); !errors.Is(err, services.ErrResetTicketNotFound) {
		t.Errorf("foreign redeem err = %v, want ErrResetTicketNotFound", err)
	}
	var otherUsed int64
	_ = pool.QueryRow(ctx, `SELECT traffic_used FROM users WHERE id = $1`, other).Scan(&otherUsed)
	if otherUsed != 700 {
		t.Errorf("foreign redeem changed the other user's usage to %d", otherUsed)
	}

	if _, err := pool.Exec(ctx, `UPDATE reset_tickets SET granted_at = NOW() - INTERVAL '2 hours', expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, ticketID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Redeem(ctx, owner, ticketID, "u@x.test"); !errors.Is(err, services.ErrResetTicketExpired) {
		t.Errorf("expired redeem err = %v, want ErrResetTicketExpired", err)
	}
	if left, _ := svc.ListUsable(ctx, owner); len(left) != 0 {
		t.Errorf("expired ticket still listed: %d", len(left))
	}
}

func TestResetTicket_ConcurrentRedeemsResetOnlyOnce(t *testing.T) {
	_, ctx, svc, _, user := resetTicketFixture(t)
	userID := user("active", 900)
	ticketID := grantOne(t, ctx, svc, userID, time.Now().Add(24*time.Hour))

	const attempts = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = svc.Redeem(ctx, userID, ticketID, "u@x.test")
		}()
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, services.ErrResetTicketUsed):
		default:
			t.Errorf("unexpected redeem error: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d redemptions succeeded, want exactly 1", ok)
	}
}

func TestResetTicket_GrantTargets(t *testing.T) {
	pool, ctx, svc, planID, user := resetTicketFixture(t)
	onPlan := user("active", 0)
	alsoOnPlan := user("active", 0)
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_expires_at = NOW() - INTERVAL '1 day' WHERE id = $1`, alsoOnPlan); err != nil {
		t.Fatal(err)
	}

	g, err := svc.Grant(ctx, services.GrantRequest{Name: "plan ticket", TargetType: services.ResetTargetPlan, PlanID: planID, ExpiresAt: time.Now().Add(48 * time.Hour)})
	if err != nil {
		t.Fatalf("plan grant: %v", err)
	}
	if g.GrantedCount != 1 {
		t.Errorf("plan grant reached %d users, want 1 (the expired term is skipped)", g.GrantedCount)
	}
	if tickets, _ := svc.ListUsable(ctx, onPlan); len(tickets) != 1 || tickets[0].Name != "plan ticket" {
		t.Errorf("plan member tickets = %+v", tickets)
	}

	all, err := svc.Grant(ctx, services.GrantRequest{Name: "everyone", TargetType: services.ResetTargetAll, ExpiresAt: time.Now().Add(48 * time.Hour)})
	if err != nil {
		t.Fatalf("all grant: %v", err)
	}
	var active int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE is_active`).Scan(&active)
	if all.GrantedCount != active {
		t.Errorf("all grant reached %d users, want every active account (%d)", all.GrantedCount, active)
	}
	_, _ = pool.Exec(context.Background(), `DELETE FROM reset_ticket_grants WHERE id = $1`, all.ID)

	if _, err := svc.Grant(ctx, services.GrantRequest{Name: "past", TargetType: services.ResetTargetUser, UserID: onPlan, ExpiresAt: time.Now().Add(-time.Minute)}); !errors.Is(err, services.ErrResetInvalidGrant) {
		t.Errorf("past expiry err = %v, want ErrResetInvalidGrant", err)
	}
	if _, err := svc.Grant(ctx, services.GrantRequest{Name: "nobody", TargetType: services.ResetTargetUser, UserID: "00000000-0000-0000-0000-000000000000", ExpiresAt: time.Now().Add(time.Hour)}); !errors.Is(err, services.ErrResetNoRecipients) {
		t.Errorf("unknown user err = %v, want ErrResetNoRecipients", err)
	}
}

func TestResetTicket_RequiresCappedRunningPlan(t *testing.T) {
	pool, ctx, svc, _, user := resetTicketFixture(t)
	userID := user("active", 300)
	ticketID := grantOne(t, ctx, svc, userID, time.Now().Add(24*time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_expires_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Redeem(ctx, userID, ticketID, "u@x.test"); !errors.Is(err, services.ErrResetNotApplicable) {
		t.Errorf("redeem on expired plan err = %v, want ErrResetNotApplicable", err)
	}
	if left, _ := svc.ListUsable(ctx, userID); len(left) != 1 {
		t.Errorf("rejected redemption consumed the ticket")
	}
}
