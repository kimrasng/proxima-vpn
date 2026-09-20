package services

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The escalation tests next door drive advance(), which is pure and never reaches
// Postgres. That leaves the half of the escalation contract that lives in SQL --
// dropping an acknowledgement taken at a lower tier -- covered by nothing, and it
// is the half that can rot silently: severity_rank() is a second copy of
// severityRank(), so the two can drift apart without either file failing to
// compile. These run the real upsert against a real database.
//
// They run only when TEST_DATABASE_URL points at a disposable Postgres, matching
// the DB-backed handler and telegram tests; CI sets it.
func alertTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed alert tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAlertNode makes a node for the alert rows to hang off, since node_alerts
// has a foreign key onto it. The name is per-test and per-process so parallel
// packages cannot collide on it.
func seedAlertNode(t *testing.T, pool *pgxpool.Pool, tag string) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("alert-db-%s-%d", tag, os.Getpid())

	var nodeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, ip, api_key, status, last_seen, shaping_ok)
		 VALUES ($1, '198.51.100.30', $2, 'online', NOW(), true)
		 RETURNING id::text`,
		name, name+"-key",
	).Scan(&nodeID); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// ON DELETE CASCADE takes the alert rows with it.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id = $1`, nodeID)
	})
	return nodeID
}

type storedAlert struct {
	state    string
	severity string
	acked    bool
	ackedBy  string
	firedAt  *time.Time
}

func readAlert(t *testing.T, pool *pgxpool.Pool, nodeID string, kind AlertKind) storedAlert {
	t.Helper()
	var got storedAlert
	var ackedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT state, severity, acked_at, acked_by, fired_at
		 FROM node_alerts WHERE node_id = $1 AND kind = $2`,
		nodeID, string(kind),
	).Scan(&got.state, &got.severity, &ackedAt, &got.ackedBy, &got.firedAt); err != nil {
		t.Fatalf("read alert row: %v", err)
	}
	got.acked = ackedAt != nil
	return got
}

// severity_rank() exists so the upsert can compare the incoming tier against the
// stored one. If it disagrees with severityRank() -- the Go function it was copied
// from -- escalations either stop dropping acknowledgements or start dropping them
// on the way down, and nothing else in the suite would notice.
func TestSeverityRankSQLAgreesWithGo(t *testing.T) {
	pool := alertTestPool(t)

	// Every severity the codebase defines, plus values the column could hold that
	// no rule produces: the function has to be total, because it is applied to
	// whatever is already stored.
	for _, s := range []Severity{
		SeverityError, SeverityWarning, SeverityInfo, SeveritySuccess, "", "bogus",
	} {
		var sqlRank int
		if err := pool.QueryRow(context.Background(),
			`SELECT severity_rank($1)`, string(s),
		).Scan(&sqlRank); err != nil {
			t.Fatalf("severity_rank(%q): %v", s, err)
		}
		if goRank := severityRank(s); sqlRank != goRank {
			t.Errorf("severity_rank(%q) = %d in SQL but %d in Go", s, sqlRank, goRank)
		}
	}

	// The ordering itself, not just the parity: the upsert's `>` comparison is only
	// meaningful if error actually outranks warning.
	var errorRank, warningRank, infoRank int
	if err := pool.QueryRow(context.Background(),
		`SELECT severity_rank('error'), severity_rank('warning'), severity_rank('info')`,
	).Scan(&errorRank, &warningRank, &infoRank); err != nil {
		t.Fatalf("query ranks: %v", err)
	}
	if errorRank <= warningRank || warningRank <= infoRank {
		t.Errorf("expected error > warning > info, got %d > %d > %d",
			errorRank, warningRank, infoRank)
	}
}

// An acknowledgement is a statement about a specific reading. When the reading
// gets materially worse the statement no longer holds, so the upsert has to clear
// it -- otherwise a node that was accepted at 82% CPU stays silently accepted
// after it reaches 96%.
func TestEscalationDropsAnAcknowledgementTakenAtTheLowerTier(t *testing.T) {
	pool := alertTestPool(t)
	nodeID := seedAlertNode(t, pool, "escalate")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertCPU)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-3 * time.Hour)

	warning := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 82, FiredAt: &fired,
	}
	if err := svc.persist(ctx, nodeID, r, warning, now); err != nil {
		t.Fatalf("persist warning: %v", err)
	}

	// The operator takes ownership at the warning tier.
	if _, err := pool.Exec(ctx,
		`UPDATE node_alerts SET acked_at = NOW(), acked_by = 'operator'
		 WHERE node_id = $1 AND kind = $2`, nodeID, string(AlertCPU),
	); err != nil {
		t.Fatalf("seed acknowledgement: %v", err)
	}

	escalated := &alertState{
		State: AlertStateFiring, Severity: SeverityError, Value: 96, FiredAt: &fired,
	}
	if err := svc.persist(ctx, nodeID, r, escalated, now.Add(15*time.Second)); err != nil {
		t.Fatalf("persist escalation: %v", err)
	}

	got := readAlert(t, pool, nodeID, AlertCPU)
	if got.severity != string(SeverityError) {
		t.Errorf("severity = %q, want error", got.severity)
	}
	if got.acked {
		t.Error("escalation must drop the acknowledgement taken at the warning tier")
	}
	if got.ackedBy != "" {
		t.Errorf("acked_by = %q, want it cleared alongside acked_at", got.ackedBy)
	}
	// The tier moved on the same episode, so its age has to survive: this is what
	// makes "3 hours" on the dashboard true rather than reset by the escalation.
	if got.firedAt == nil || !got.firedAt.Equal(fired) {
		t.Errorf("fired_at = %v, want it unchanged at %v", got.firedAt, fired)
	}
	if got.state != AlertStateFiring {
		t.Errorf("state = %q, want firing", got.state)
	}
}

// Coming back down asserts nothing new, so the acknowledgement still stands. If
// the comparison were >= instead of >, every steady sample would clear the ack
// and the operator could never make one stick.
func TestDeescalationAndSteadySamplesKeepTheAcknowledgement(t *testing.T) {
	pool := alertTestPool(t)
	nodeID := seedAlertNode(t, pool, "deescalate")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertCPU)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-time.Hour)

	errorState := &alertState{
		State: AlertStateFiring, Severity: SeverityError, Value: 96, FiredAt: &fired,
	}
	if err := svc.persist(ctx, nodeID, r, errorState, now); err != nil {
		t.Fatalf("persist error tier: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE node_alerts SET acked_at = NOW(), acked_by = 'operator'
		 WHERE node_id = $1 AND kind = $2`, nodeID, string(AlertCPU),
	); err != nil {
		t.Fatalf("seed acknowledgement: %v", err)
	}

	// Same tier again, as the evaluator writes on every tick.
	steady := &alertState{
		State: AlertStateFiring, Severity: SeverityError, Value: 95, FiredAt: &fired,
	}
	if err := svc.persist(ctx, nodeID, r, steady, now.Add(15*time.Second)); err != nil {
		t.Fatalf("persist steady sample: %v", err)
	}
	if got := readAlert(t, pool, nodeID, AlertCPU); !got.acked {
		t.Error("an unchanged tier must not clear the acknowledgement")
	}

	lower := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 85, FiredAt: &fired,
	}
	if err := svc.persist(ctx, nodeID, r, lower, now.Add(30*time.Second)); err != nil {
		t.Fatalf("persist de-escalation: %v", err)
	}

	got := readAlert(t, pool, nodeID, AlertCPU)
	if got.severity != string(SeverityWarning) {
		t.Errorf("severity = %q, want warning", got.severity)
	}
	if !got.acked {
		t.Error("de-escalation asserts nothing new and must keep the acknowledgement")
	}
	if got.ackedBy != "operator" {
		t.Errorf("acked_by = %q, want it preserved", got.ackedBy)
	}
}

// An acknowledgement belongs to the episode it was made against. Resolving ends
// that episode, so a later re-fire has to start unacknowledged rather than
// inheriting a decision made about a condition that has since come and gone.
func TestResolvingClearsTheAcknowledgementSoARefireIsUnacknowledged(t *testing.T) {
	pool := alertTestPool(t)
	nodeID := seedAlertNode(t, pool, "resolve")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertCPU)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-time.Hour)

	firing := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 82, FiredAt: &fired,
	}
	if err := svc.persist(ctx, nodeID, r, firing, now); err != nil {
		t.Fatalf("persist firing: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE node_alerts SET acked_at = NOW(), acked_by = 'operator'
		 WHERE node_id = $1 AND kind = $2`, nodeID, string(AlertCPU),
	); err != nil {
		t.Fatalf("seed acknowledgement: %v", err)
	}

	resolved := now.Add(15 * time.Second)
	ok := &alertState{
		State: AlertStateOK, Severity: SeverityWarning, Value: 40, ResolvedAt: &resolved,
	}
	if err := svc.persist(ctx, nodeID, r, ok, resolved); err != nil {
		t.Fatalf("persist resolve: %v", err)
	}

	got := readAlert(t, pool, nodeID, AlertCPU)
	if got.acked {
		t.Error("resolving must clear the acknowledgement for the episode that ended")
	}
	if got.ackedBy != "" {
		t.Errorf("acked_by = %q, want it cleared on resolve", got.ackedBy)
	}
}

// persist() names ten columns and the upsert repeats each one in its DO UPDATE
// clause. A column added to the table but forgotten in the write is invisible to
// the compiler and to vet, so this drives the real statement and reads back every
// field the evaluator owns.
func TestPersistWritesEveryFieldTheEvaluatorOwns(t *testing.T) {
	pool := alertTestPool(t)
	nodeID := seedAlertNode(t, pool, "roundtrip")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertDisk)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	breach := now.Add(-10 * time.Minute)
	clear := now.Add(-2 * time.Minute)
	fired := now.Add(-5 * time.Minute)

	st := &alertState{
		State:       AlertStateFiring,
		Severity:    SeverityError,
		Value:       98,
		BreachSince: &breach,
		ClearSince:  &clear,
		FiredAt:     &fired,
	}
	if err := svc.persist(ctx, nodeID, r, st, now); err != nil {
		t.Fatalf("persist: %v", err)
	}

	var (
		state, severity        string
		value                  float64
		breachOut, clearOut    *time.Time
		firedOut, evaluatedOut *time.Time
	)
	if err := pool.QueryRow(ctx,
		`SELECT state, severity, value, breach_since, clear_since, fired_at, evaluated_at
		 FROM node_alerts WHERE node_id = $1 AND kind = $2`,
		nodeID, string(AlertDisk),
	).Scan(&state, &severity, &value, &breachOut, &clearOut, &firedOut, &evaluatedOut); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if state != AlertStateFiring {
		t.Errorf("state = %q, want firing", state)
	}
	// persist writes st.Severity rather than the rule's static level, which is what
	// lets an escalated row keep its tier across ticks.
	if severity != string(SeverityError) {
		t.Errorf("severity = %q, want the state's escalated tier, not the rule's %q",
			severity, r.severity)
	}
	if value != 98 {
		t.Errorf("value = %v, want 98", value)
	}
	for name, pair := range map[string][2]*time.Time{
		"breach_since": {breachOut, &breach},
		"clear_since":  {clearOut, &clear},
		"fired_at":     {firedOut, &fired},
		"evaluated_at": {evaluatedOut, &now},
	} {
		got, want := pair[0], pair[1]
		if got == nil || !got.Equal(*want) {
			t.Errorf("%s = %v, want %v", name, got, *want)
		}
	}

	// evaluated_at is also pushed back onto the state, because the next tick's gap
	// calculation reads it.
	if st.EvaluatedAt == nil || !st.EvaluatedAt.Equal(now) {
		t.Errorf("persist must stamp EvaluatedAt on the state, got %v", st.EvaluatedAt)
	}
}
