package services

import (
	"context"
	"errors"
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

// seedAlertNode makes a node for the alert rows to describe. The name is per-test
// and per-process so parallel packages cannot collide on it.
func seedAlertNode(t *testing.T, pool *pgxpool.Pool, tag string) nodeReading {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("alert-db-%s-%d", tag, os.Getpid())

	var nodeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, ip, api_key, status, last_seen, shaping_ok, country, region)
		 VALUES ($1, '198.51.100.30', $2, 'online', NOW(), true, 'JP', 'Tokyo')
		 RETURNING id::text`,
		name, name+"-key",
	).Scan(&nodeID); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// node_alerts no longer cascades, so the alert rows have to go explicitly.
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM node_alerts WHERE node_id = $1`, nodeID)
		_, _ = pool.Exec(bg, `DELETE FROM nodes WHERE id = $1`, nodeID)
	})
	return nodeReading{id: nodeID, name: name, country: "JP", region: "Tokyo", status: "online"}
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
	node := seedAlertNode(t, pool, "escalate")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertCPU)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-3 * time.Hour)

	warning := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 82, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, warning, now); err != nil {
		t.Fatalf("persist warning: %v", err)
	}

	// The operator takes ownership at the warning tier.
	if _, err := pool.Exec(ctx,
		`UPDATE node_alerts SET acked_at = NOW(), acked_by = 'operator'
		 WHERE node_id = $1 AND kind = $2`, node.id, string(AlertCPU),
	); err != nil {
		t.Fatalf("seed acknowledgement: %v", err)
	}

	escalated := &alertState{
		State: AlertStateFiring, Severity: SeverityError, Value: 96, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, escalated, now.Add(15*time.Second)); err != nil {
		t.Fatalf("persist escalation: %v", err)
	}

	got := readAlert(t, pool, node.id, AlertCPU)
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
	node := seedAlertNode(t, pool, "deescalate")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertCPU)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-time.Hour)

	errorState := &alertState{
		State: AlertStateFiring, Severity: SeverityError, Value: 96, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, errorState, now); err != nil {
		t.Fatalf("persist error tier: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE node_alerts SET acked_at = NOW(), acked_by = 'operator'
		 WHERE node_id = $1 AND kind = $2`, node.id, string(AlertCPU),
	); err != nil {
		t.Fatalf("seed acknowledgement: %v", err)
	}

	// Same tier again, as the evaluator writes on every tick.
	steady := &alertState{
		State: AlertStateFiring, Severity: SeverityError, Value: 95, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, steady, now.Add(15*time.Second)); err != nil {
		t.Fatalf("persist steady sample: %v", err)
	}
	if got := readAlert(t, pool, node.id, AlertCPU); !got.acked {
		t.Error("an unchanged tier must not clear the acknowledgement")
	}

	lower := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 85, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, lower, now.Add(30*time.Second)); err != nil {
		t.Fatalf("persist de-escalation: %v", err)
	}

	got := readAlert(t, pool, node.id, AlertCPU)
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
	node := seedAlertNode(t, pool, "resolve")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertCPU)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-time.Hour)

	firing := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 82, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, firing, now); err != nil {
		t.Fatalf("persist firing: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE node_alerts SET acked_at = NOW(), acked_by = 'operator'
		 WHERE node_id = $1 AND kind = $2`, node.id, string(AlertCPU),
	); err != nil {
		t.Fatalf("seed acknowledgement: %v", err)
	}

	resolved := now.Add(15 * time.Second)
	ok := &alertState{
		State: AlertStateOK, Severity: SeverityWarning, Value: 40, ResolvedAt: &resolved,
	}
	if err := svc.persist(ctx, node, r, ok, resolved); err != nil {
		t.Fatalf("persist resolve: %v", err)
	}

	got := readAlert(t, pool, node.id, AlertCPU)
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
	node := seedAlertNode(t, pool, "roundtrip")

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
	if err := svc.persist(ctx, node, r, st, now); err != nil {
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
		node.id, string(AlertDisk),
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

// findOpen locates one alert in the ListOpen result, which is the read path the
// dashboard actually uses.
func findOpen(t *testing.T, svc *AlertService, alertID string) (OpenAlert, bool) {
	t.Helper()
	open, err := svc.ListOpen(context.Background())
	if err != nil {
		t.Fatalf("list open alerts: %v", err)
	}
	for _, o := range open {
		if o.ID == alertID {
			return o, true
		}
	}
	return OpenAlert{}, false
}

func alertIDFor(t *testing.T, pool *pgxpool.Pool, nodeID string, kind AlertKind) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM node_alerts WHERE node_id = $1 AND kind = $2`,
		nodeID, string(kind),
	).Scan(&id); err != nil {
		t.Fatalf("read alert id: %v", err)
	}
	return id
}

// An alert records something that happened. Retiring the node it happened on used
// to erase it outright - a foreign key with ON DELETE CASCADE - and the read path
// joined the node for its name, so even a surviving row would have dropped out of
// the list. Both of those are what this covers.
func TestAnAlertOutlivesTheNodeItDescribes(t *testing.T) {
	pool := alertTestPool(t)
	node := seedAlertNode(t, pool, "outlive")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM node_alerts WHERE node_id = $1`, node.id)
	})

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertDisk)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-2 * time.Hour)

	st := &alertState{
		State: AlertStateFiring, Severity: SeverityError, Value: 98, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, st, now); err != nil {
		t.Fatalf("persist: %v", err)
	}
	alertID := alertIDFor(t, pool, node.id, AlertDisk)

	if _, err := pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, node.id); err != nil {
		t.Fatalf("delete node: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_alerts WHERE id = $1`, alertID,
	).Scan(&count); err != nil {
		t.Fatalf("count alert rows: %v", err)
	}
	if count != 1 {
		t.Fatal("deleting a node must not delete the alerts it raised")
	}

	got, ok := findOpen(t, svc, alertID)
	if !ok {
		t.Fatal("an alert whose node is gone must still be listed, or nobody can act on it")
	}
	// The snapshot is the whole point: with the node row gone there is nothing to
	// join to, so without it the row could not even say what it was about.
	if got.NodeName != node.name {
		t.Errorf("node_name = %q, want the snapshot %q", got.NodeName, node.name)
	}
	if got.Country != "JP" || got.Region != "Tokyo" {
		t.Errorf("location = %q/%q, want the snapshot JP/Tokyo", got.Country, got.Region)
	}
	if !got.NodeDeleted {
		t.Error("NodeDeleted must mark an alert whose node no longer exists")
	}
	if got.NodeStatus != "" {
		t.Errorf("NodeStatus = %q, want empty for a deleted node", got.NodeStatus)
	}
	if got.Value != 98 || got.Severity != string(SeverityError) {
		t.Errorf("reading = %v/%s, want the frozen 98/error", got.Value, got.Severity)
	}
}

// The snapshot is rewritten every tick, so it tracks a rename while the node is
// alive rather than freezing whatever was true when the alert first fired.
func TestTheNodeSnapshotFollowsARenameWhileTheNodeExists(t *testing.T) {
	pool := alertTestPool(t)
	node := seedAlertNode(t, pool, "rename")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertCPU)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-time.Hour)

	st := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 82, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, st, now); err != nil {
		t.Fatalf("persist: %v", err)
	}

	renamed := node
	renamed.name = node.name + "-renamed"
	renamed.region = "Osaka"
	if err := svc.persist(ctx, renamed, r, st, now.Add(15*time.Second)); err != nil {
		t.Fatalf("persist after rename: %v", err)
	}

	got, ok := findOpen(t, svc, alertIDFor(t, pool, node.id, AlertCPU))
	if !ok {
		t.Fatal("alert missing from the open list")
	}
	if got.NodeName != renamed.name {
		t.Errorf("node_name = %q, want the current %q", got.NodeName, renamed.name)
	}
	if got.Region != "Osaka" {
		t.Errorf("node_region = %q, want the current Osaka", got.Region)
	}
}

// Close is the only exit for an alert the evaluator cannot resolve. It is
// restricted to exactly those, because clearing a live firing row would be undone
// on the next tick and would reset the episode's age on the way.
func TestCloseAcceptsOnlyWhatTheEvaluatorCannotResolve(t *testing.T) {
	pool := alertTestPool(t)
	svc := NewAlertService(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-time.Hour)

	t.Run("refuses a firing alert on a live node", func(t *testing.T) {
		node := seedAlertNode(t, pool, "closefiring")
		r := ruleFor(t, AlertCPU)
		st := &alertState{
			State: AlertStateFiring, Severity: SeverityWarning, Value: 82, FiredAt: &fired,
		}
		if err := svc.persist(ctx, node, r, st, now); err != nil {
			t.Fatalf("persist: %v", err)
		}
		alertID := alertIDFor(t, pool, node.id, AlertCPU)

		if _, err := svc.Close(ctx, alertID, "operator"); !errors.Is(err, ErrAlertNotClosable) {
			t.Fatalf("want ErrAlertNotClosable, got %v", err)
		}
		got := readAlert(t, pool, node.id, AlertCPU)
		if got.state != AlertStateFiring {
			t.Errorf("a refused close must leave the row alone, state = %q", got.state)
		}
		if got.firedAt == nil || !got.firedAt.Equal(fired) {
			t.Errorf("a refused close must not touch fired_at, got %v", got.firedAt)
		}
	})

	t.Run("closes a stale alert and ends its episode", func(t *testing.T) {
		node := seedAlertNode(t, pool, "closestale")
		r := ruleFor(t, AlertDisk)
		st := &alertState{
			State: AlertStateStale, Severity: SeverityWarning, Value: 95, FiredAt: &fired,
		}
		if err := svc.persist(ctx, node, r, st, now); err != nil {
			t.Fatalf("persist: %v", err)
		}
		alertID := alertIDFor(t, pool, node.id, AlertDisk)
		if _, err := pool.Exec(ctx,
			`UPDATE node_alerts SET acked_at = NOW(), acked_by = 'operator',
			        silenced_until = NOW() + INTERVAL '30 minutes'
			 WHERE id = $1`, alertID,
		); err != nil {
			t.Fatalf("seed ack and silence: %v", err)
		}

		closed, err := svc.Close(ctx, alertID, "operator")
		if err != nil {
			t.Fatalf("close a stale alert: %v", err)
		}
		if closed.NodeName != node.name {
			t.Errorf("returned node name = %q, want %q", closed.NodeName, node.name)
		}

		var (
			state, closedBy string
			closedAt        *time.Time
			firedAt         *time.Time
			ackedAt         *time.Time
			silenced        *time.Time
		)
		if err := pool.QueryRow(ctx,
			`SELECT state, closed_by, closed_at, fired_at, acked_at, silenced_until
			 FROM node_alerts WHERE id = $1`, alertID,
		).Scan(&state, &closedBy, &closedAt, &firedAt, &ackedAt, &silenced); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if state != AlertStateOK {
			t.Errorf("state = %q, want ok so the row leaves the open list", state)
		}
		if closedAt == nil || closedBy != "operator" {
			t.Errorf("closure must be attributed, got %v by %q", closedAt, closedBy)
		}
		// The episode is over. Leaving fired_at set would let a later breach
		// resume this one and report an age measured from the retired problem.
		if firedAt != nil {
			t.Errorf("fired_at = %v, want cleared so a later breach fires anew", firedAt)
		}
		if ackedAt != nil || silenced != nil {
			t.Error("the acknowledgement and silence belonged to the closed episode")
		}

		if _, ok := findOpen(t, svc, alertID); ok {
			t.Error("a closed alert must not appear in the open list")
		}
	})

	t.Run("closes a firing alert once its node is gone", func(t *testing.T) {
		node := seedAlertNode(t, pool, "closeorphan")
		r := ruleFor(t, AlertCPU)
		st := &alertState{
			State: AlertStateFiring, Severity: SeverityError, Value: 96, FiredAt: &fired,
		}
		if err := svc.persist(ctx, node, r, st, now); err != nil {
			t.Fatalf("persist: %v", err)
		}
		alertID := alertIDFor(t, pool, node.id, AlertCPU)
		if _, err := pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, node.id); err != nil {
			t.Fatalf("delete node: %v", err)
		}

		// Firing would normally be refused; with no node left, no reading can
		// ever contradict the closure.
		if _, err := svc.Close(ctx, alertID, "operator"); err != nil {
			t.Fatalf("close an orphaned alert: %v", err)
		}
		if _, ok := findOpen(t, svc, alertID); ok {
			t.Error("a closed orphan must not appear in the open list")
		}
	})

	t.Run("reports a missing alert apart from a refused one", func(t *testing.T) {
		_, err := svc.Close(ctx, "00000000-0000-0000-0000-000000000000", "operator")
		if err == nil || errors.Is(err, ErrAlertNotClosable) {
			t.Fatalf("an unknown id is not a refusal, got %v", err)
		}
	})
}

// Closing retires one episode, not the condition. A node that comes back and
// breaches again has to fire as new, which means the closure stamp must not linger
// and claim the fresh problem was already dealt with.
func TestARefireAfterCloseIsANewEpisode(t *testing.T) {
	pool := alertTestPool(t)
	node := seedAlertNode(t, pool, "refire")

	svc := NewAlertService(pool)
	r := ruleFor(t, AlertDisk)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	fired := now.Add(-time.Hour)

	stale := &alertState{
		State: AlertStateStale, Severity: SeverityWarning, Value: 95, FiredAt: &fired,
	}
	if err := svc.persist(ctx, node, r, stale, now); err != nil {
		t.Fatalf("persist stale: %v", err)
	}
	alertID := alertIDFor(t, pool, node.id, AlertDisk)
	if _, err := svc.Close(ctx, alertID, "operator"); err != nil {
		t.Fatalf("close: %v", err)
	}

	refired := now.Add(time.Hour)
	fresh := &alertState{
		State: AlertStateFiring, Severity: SeverityWarning, Value: 93, FiredAt: &refired,
	}
	if err := svc.persist(ctx, node, r, fresh, refired); err != nil {
		t.Fatalf("persist refire: %v", err)
	}

	var (
		state, closedBy string
		closedAt        *time.Time
		firedAt         *time.Time
	)
	if err := pool.QueryRow(ctx,
		`SELECT state, closed_by, closed_at, fired_at FROM node_alerts WHERE id = $1`, alertID,
	).Scan(&state, &closedBy, &closedAt, &firedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != AlertStateFiring {
		t.Errorf("state = %q, want firing", state)
	}
	if closedAt != nil || closedBy != "" {
		t.Errorf("a new episode must not carry the old closure, got %v by %q", closedAt, closedBy)
	}
	if firedAt == nil || !firedAt.Equal(refired) {
		t.Errorf("fired_at = %v, want the new episode's %v", firedAt, refired)
	}
	if _, ok := findOpen(t, svc, alertID); !ok {
		t.Error("the refired alert must be back in the open list")
	}
}
