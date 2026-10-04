package services_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// grantEvidence reads the pair of audit timestamps a settled order is
// supposed to carry. Either may be NULL, so both come back nullable.
func grantEvidence(t *testing.T, pool *pgxpool.Pool, ctx context.Context, orderID string) (*time.Time, *time.Time) {
	t.Helper()
	var before, after *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT granted_plan_expires_before, granted_plan_expires_after FROM plan_orders WHERE id = $1`,
		orderID,
	).Scan(&before, &after); err != nil {
		t.Fatalf("read grant evidence: %v", err)
	}
	return before, after
}

// The prior term's expiry is what a paid order's audit trail has to show
// alongside the resulting one, and only the grant itself can read it without
// racing another grant. A renewal's before value is the exact timestamp the
// row carried going in - seeded here at an odd 17-day offset so it cannot be
// confused with the resulting expiry or with NOW().
func TestGrantPlanWithDuration_ReportsPriorExpiryAsBefore(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("beforerenew-%d", os.Getpid()), 30, nil)
	start := time.Now().Add(-10 * 24 * time.Hour)
	expires := time.Now().Add(17 * 24 * time.Hour)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("beforerenew-%d@x.test", os.Getpid()), planID, &start, &expires, nil, 0, "active")

	result, err := svc.GrantPlanWithDuration(ctx, userID, planID, 90)
	if err != nil {
		t.Fatalf("GrantPlanWithDuration: %v", err)
	}
	if result.PlanExpiresBefore == nil {
		t.Fatal("PlanExpiresBefore is nil, want the seeded prior expiry (the user had a term)")
	}
	if diff := result.PlanExpiresBefore.Sub(expires); diff < -time.Second || diff > time.Second {
		t.Errorf("PlanExpiresBefore = %v, want seeded %v (the term as it stood before the grant)",
			result.PlanExpiresBefore, expires)
	}
}

// No prior term at all must report a nil before value, not NOW() - a NULL
// before timestamp is what distinguishes "never had a term" from "had one
// that expired", and the latter carries a real past timestamp.
func TestGrantPlanWithDuration_FirstGrantReportsNilBefore(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("beforefirst-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("beforefirst-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")

	result, err := svc.GrantPlanWithDuration(ctx, userID, planID, 30)
	if err != nil {
		t.Fatalf("GrantPlanWithDuration: %v", err)
	}
	if result.PlanExpiresBefore != nil {
		t.Errorf("PlanExpiresBefore = %v, want nil (no prior term to report)", result.PlanExpiresBefore)
	}
}

// An expired prior term is a distinct case from no term: the grant computes
// its new expiry from NOW() rather than the old value, but the before value
// must still be the expired timestamp the row actually carried.
func TestGrantPlanWithDuration_ExpiredPriorTermReportsPastBefore(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPlanService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("beforeexpired-%d", os.Getpid()), 30, nil)
	start := time.Now().Add(-40 * 24 * time.Hour)
	expires := time.Now().Add(-11 * 24 * time.Hour)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("beforeexpired-%d@x.test", os.Getpid()), planID, &start, &expires, nil, 0, "expired")

	result, err := svc.GrantPlanWithDuration(ctx, userID, planID, 30)
	if err != nil {
		t.Fatalf("GrantPlanWithDuration: %v", err)
	}
	if result.Kind != services.GrantFresh {
		t.Errorf("Kind = %q, want %q", result.Kind, services.GrantFresh)
	}
	if result.PlanExpiresBefore == nil {
		t.Fatal("PlanExpiresBefore is nil, want the expired prior expiry (an expired term is not an absent one)")
	}
	if diff := result.PlanExpiresBefore.Sub(expires); diff < -time.Second || diff > time.Second {
		t.Errorf("PlanExpiresBefore = %v, want seeded %v", result.PlanExpiresBefore, expires)
	}
}

// A settled order is the audit record of what the customer bought, so both
// ends of the term it moved have to land on the row itself. The prior expiry
// is seeded 23 days out - distinct from both NOW() and the resulting expiry -
// so a recorded value that merely echoes the new term would not pass.
func TestSettle_RecordsGrantExpiryEvidence(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("evidence-renew-%d", os.Getpid()), 30, nil)
	start := time.Now().Add(-10 * 24 * time.Hour)
	priorExpiry := time.Now().Add(23 * 24 * time.Hour)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("evidence-renew-%d@x.test", os.Getpid()), planID, &start, &priorExpiry, nil, 0, "active")
	orderID := seedOrder(t, pool, ctx, userID, planID, 90, 2700, "pending", nil)

	res, err := svc.Settle(ctx, payments.Confirmation{
		Provider:   "stripe",
		ExternalID: fmt.Sprintf("evt_evidence_renew_%d", os.Getpid()),
		OrderID:    orderID,
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleGranted {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, services.SettleGranted)
	}

	before, after := grantEvidence(t, pool, ctx, orderID)
	if before == nil {
		t.Fatal("granted_plan_expires_before is NULL, want the seeded prior expiry")
	}
	if diff := before.Sub(priorExpiry); diff < -time.Second || diff > time.Second {
		t.Errorf("granted_plan_expires_before = %v, want seeded %v", before, priorExpiry)
	}
	if after == nil {
		t.Fatal("granted_plan_expires_after is NULL, want the resulting expiry")
	}
	wantAfter := priorExpiry.Add(90 * 24 * time.Hour)
	if diff := after.Sub(wantAfter); diff < -time.Second || diff > time.Second {
		t.Errorf("granted_plan_expires_after = %v, want ~%v (prior expiry + the order's 90 days)", after, wantAfter)
	}
}

// A first purchase must record a NULL before and a real after: NULL is the
// only way the audit trail can say "no prior term" rather than claiming the
// customer's term began at the moment of payment.
func TestSettle_FirstPurchaseRecordsNullBeforeEvidence(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("evidence-first-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("evidence-first-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "pending", nil)

	settledAt := time.Now()
	res, err := svc.Settle(ctx, payments.Confirmation{
		Provider:   "stripe",
		ExternalID: fmt.Sprintf("evt_evidence_first_%d", os.Getpid()),
		OrderID:    orderID,
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleGranted {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, services.SettleGranted)
	}

	before, after := grantEvidence(t, pool, ctx, orderID)
	if before != nil {
		t.Errorf("granted_plan_expires_before = %v, want NULL (no prior term)", before)
	}
	if after == nil {
		t.Fatal("granted_plan_expires_after is NULL, want the resulting expiry")
	}
	wantAfter := settledAt.Add(30 * 24 * time.Hour)
	if diff := after.Sub(wantAfter); diff < -time.Minute || diff > time.Minute {
		t.Errorf("granted_plan_expires_after = %v, want ~%v (NOW + the order's 30 days)", after, wantAfter)
	}
}

// An order that never granted must carry no evidence at all - recording a
// term for a rejected payment would be a false financial fact.
func TestSettle_IgnoredOrderRecordsNoEvidence(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	svc := services.NewPaymentService(pool)

	planID := seedPlan(t, pool, ctx, fmt.Sprintf("evidence-ignored-%d", os.Getpid()), 30, nil)
	userID := seedUser(t, pool, ctx, fmt.Sprintf("evidence-ignored-%d@x.test", os.Getpid()), planID, nil, nil, nil, 0, "pending")
	orderID := seedOrder(t, pool, ctx, userID, planID, 30, 1000, "cancelled", nil)

	res, err := svc.Settle(ctx, payments.Confirmation{
		Provider:   "stripe",
		ExternalID: fmt.Sprintf("evt_evidence_ignored_%d", os.Getpid()),
		OrderID:    orderID,
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if res.Outcome != services.SettleIgnored {
		t.Fatalf("Outcome = %q, want %q", res.Outcome, services.SettleIgnored)
	}

	before, after := grantEvidence(t, pool, ctx, orderID)
	if before != nil || after != nil {
		t.Errorf("evidence on an ungranted order = %v/%v, want NULL/NULL", before, after)
	}
}
