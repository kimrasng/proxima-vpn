package services

import (
	"testing"
	"time"
)

func ruleFor(t *testing.T, kind AlertKind) alertRule {
	t.Helper()
	for _, r := range alertRules {
		if r.kind == kind {
			return r
		}
	}
	t.Fatalf("no rule for kind %q", kind)
	return alertRule{}
}

// seeded returns a state that has already been observed once, so transitions from
// it are real edges rather than first-observation seeds.
func seeded(state string) *alertState {
	return &alertState{State: state, exists: true}
}

// The rule table is the one place thresholds are declared, and the state machine
// trusts it rather than re-checking these relations on every sample. Asserting
// them here is what keeps that trust honest: a table edited into an inconsistent
// shape fails in CI instead of producing an alert that flaps or never clears.
func TestAlertRulesAreWellFormed(t *testing.T) {
	for _, r := range alertRules {
		if r.severity == "" {
			t.Errorf("%s: no severity", r.kind)
		}
		if r.clearFor <= 0 {
			t.Errorf("%s: clearFor must be positive, or a recovery never settles", r.kind)
		}

		if !r.numeric {
			// A boolean rule is read from a flag, so a threshold on it would be
			// silently ignored rather than wrong-but-visible.
			if r.fire != 0 || r.clear != 0 || r.escalate != 0 || r.deescalate != 0 {
				t.Errorf("%s: boolean rule carries numeric thresholds", r.kind)
			}
			if r.escalateSeverity != "" {
				t.Errorf("%s: boolean rule carries an escalation tier", r.kind)
			}
			continue
		}

		if r.clear >= r.fire {
			t.Errorf("%s: clear %.0f must sit below fire %.0f, or the band cannot damp a value at the boundary",
				r.kind, r.clear, r.fire)
		}

		if r.escalateSeverity == "" {
			if r.escalate != 0 || r.deescalate != 0 {
				t.Errorf("%s: escalation thresholds set without an escalated severity", r.kind)
			}
			continue
		}
		if r.escalate <= 0 {
			t.Errorf("%s: escalateSeverity set without an escalate threshold", r.kind)
		}
		if r.deescalate >= r.escalate {
			t.Errorf("%s: deescalate %.0f must sit below escalate %.0f, for the same reason clear sits below fire",
				r.kind, r.deescalate, r.escalate)
		}
		if r.escalate < r.fire {
			t.Errorf("%s: escalate %.0f below fire %.0f would tier before the alert exists",
				r.kind, r.escalate, r.fire)
		}
		if r.deescalate < r.clear {
			t.Errorf("%s: deescalate %.0f below clear %.0f would resolve before de-escalating",
				r.kind, r.deescalate, r.clear)
		}
		if severityRank(r.escalateSeverity) <= severityRank(r.severity) {
			t.Errorf("%s: escalateSeverity %q does not outrank %q", r.kind, r.escalateSeverity, r.severity)
		}
	}
}

// TST-004: a value oscillating around the fire threshold must produce one firing
// episode, and must not resolve until it holds below the clear threshold.
func TestOscillationProducesSingleFiringEpisode(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	fires, resolves := 0, 0
	// Hold above the fire threshold long enough to fire.
	for i := 0; i < 40; i++ {
		if e := st.advance(r, r.observe(true, 81, false), now); e != nil {
			if e.To == AlertStateFiring {
				fires++
			} else {
				resolves++
			}
		}
		now = now.Add(15 * time.Second)
	}
	if fires != 1 {
		t.Fatalf("expected exactly 1 fire, got %d", fires)
	}

	// Now oscillate inside the hysteresis band: 79.6 is below the fire threshold
	// but above the clear threshold, so nothing may change.
	for i := 0; i < 40; i++ {
		v := 79.6
		if i%2 == 0 {
			v = 80.4
		}
		if e := st.advance(r, r.observe(true, v, false), now); e != nil {
			t.Fatalf("band oscillation produced edge %+v at i=%d", e, i)
		}
		now = now.Add(15 * time.Second)
	}
	if st.State != AlertStateFiring {
		t.Fatalf("expected still firing inside band, got %q", st.State)
	}

	// A genuine drop below the clear threshold resolves once the clear duration
	// elapses.
	for i := 0; i < 40; i++ {
		if e := st.advance(r, r.observe(true, 40, false), now); e != nil {
			if e.To == AlertStateOK {
				resolves++
			}
		}
		now = now.Add(15 * time.Second)
	}
	if resolves != 1 {
		t.Fatalf("expected exactly 1 resolve, got %d", resolves)
	}
	if st.State != AlertStateOK {
		t.Fatalf("expected ok after sustained clear, got %q", st.State)
	}
}

// TST-004 (duration): a breach shorter than the fire duration must stay pending.
func TestShortBreachDoesNotFire(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 4; i++ {
		if e := st.advance(r, r.observe(true, 92, false), now); e != nil {
			t.Fatalf("fired after only %s, expected to wait %s", time.Duration(i)*15*time.Second, r.fireFor)
		}
		now = now.Add(15 * time.Second)
	}
	if st.State != AlertStatePending {
		t.Fatalf("expected pending during a short breach, got %q", st.State)
	}
}

// TST-005: every breached rule on one node is reported, not just the first.
func TestConcurrentBreachesOnOneNode(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	offline := ruleFor(t, AlertOffline)
	disk := ruleFor(t, AlertDisk)

	offState := seeded(AlertStateOK)
	diskState := seeded(AlertStateOK)

	// A node that is offline AND was last seen above its disk threshold. Disk is
	// a resource rule, so it freezes rather than firing while offline; the point
	// of this test is that the two rules are evaluated independently.
	if e := offState.advance(offline, offline.observe(false, 0, true), now); e == nil || e.To != AlertStateFiring {
		t.Fatalf("offline rule should fire immediately, got %+v", e)
	}
	if diskState.State == AlertStateFiring {
		t.Fatal("disk should not fire while the node is not reporting")
	}

	// Once the node reports again while still above the disk threshold, the disk
	// alert fires on its own, independently of the offline alert.
	if e := diskState.advance(disk, disk.observe(true, 92, false), now); e == nil || e.To != AlertStateFiring {
		t.Fatalf("disk rule should fire once the node reports, got %+v", e)
	}
}

// TST-006: a firing resource alert freezes as stale when the node stops
// reporting, and must not emit a resolve.
func TestOfflineFreezesResourceAlertAsStale(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 40; i++ {
		st.advance(r, r.observe(true, 92, false), now)
		now = now.Add(15 * time.Second)
	}
	if st.State != AlertStateFiring {
		t.Fatalf("setup failed: expected firing, got %q", st.State)
	}
	firedAt := *st.FiredAt

	if e := st.advance(r, r.observe(false, 92, false), now); e != nil {
		t.Fatalf("going offline must not emit an edge, got %+v", e)
	}
	if st.State != AlertStateStale {
		t.Fatalf("expected stale, got %q", st.State)
	}
	if st.FiredAt == nil || !st.FiredAt.Equal(firedAt) {
		t.Fatalf("stale must preserve fired_at, got %v want %v", st.FiredAt, firedAt)
	}

	// Coming back still breaching resumes the same episode: the age must not
	// reset, because the condition never went away.
	now = now.Add(time.Hour)
	for i := 0; i < 40; i++ {
		st.advance(r, r.observe(true, 92, false), now)
		now = now.Add(15 * time.Second)
	}
	if st.FiredAt == nil || !st.FiredAt.Equal(firedAt) {
		t.Fatalf("resumed episode must keep its original fired_at, got %v want %v", st.FiredAt, firedAt)
	}
}

// TST-007: the first observation of an already-breaching condition seeds state
// without notifying, so a first deploy does not page for every existing problem.
func TestFirstObservationSeedsWithoutNotifying(t *testing.T) {
	r := ruleFor(t, AlertOffline)
	st := &alertState{State: AlertStateOK} // never evaluated: exists == false
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	e := st.advance(r, r.observe(false, 0, true), now)
	if e == nil {
		t.Fatal("expected the seed to record a firing state")
	}
	if e.Notify {
		t.Fatal("a seeded first observation must not notify")
	}
	if st.State != AlertStateFiring {
		t.Fatalf("expected firing, got %q", st.State)
	}

	// A later real edge on the same row does notify.
	st2 := seeded(AlertStateOK)
	if e := st2.advance(r, r.observe(false, 0, true), now); e == nil || !e.Notify {
		t.Fatalf("an edge on an existing row must notify, got %+v", e)
	}
}

// TST-008: a gap in evaluation resets the sustained-breach window, so time nobody
// sampled cannot satisfy a for-duration.
func TestEvaluationGapResetsBreachWindow(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now

	st := seeded(AlertStateOK)
	st.BreachSince = &start
	st.EvaluatedAt = &start

	// Ten minutes later with nothing observed in between: the old breach_since
	// would satisfy the 5-minute duration on the very first sample back.
	now = now.Add(10 * time.Minute)
	st.gapReset(now, alertMaxGap)
	if st.BreachSince != nil {
		t.Fatal("a gap longer than maxGap must clear breach_since")
	}
	if e := st.advance(r, r.observe(true, 92, false), now); e != nil {
		t.Fatalf("the first sample after a gap must not fire, got %+v", e)
	}

	// A gap inside the tolerance leaves the window intact.
	st2 := seeded(AlertStatePending)
	st2.BreachSince = &start
	within := start.Add(20 * time.Second)
	st2.EvaluatedAt = &within
	st2.gapReset(within.Add(10*time.Second), alertMaxGap)
	if st2.BreachSince == nil {
		t.Fatal("a gap within tolerance must preserve breach_since")
	}
}

// Disk has no fire duration because the value moves over hours; assert it fires
// on the first breach rather than waiting.
func TestDiskFiresImmediately(t *testing.T) {
	r := ruleFor(t, AlertDisk)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	e := st.advance(r, r.observe(true, 92, false), now)
	if e == nil || e.To != AlertStateFiring {
		t.Fatalf("disk should fire on first breach, got %+v", e)
	}
}

// Offline clears only after the recovery window, so a node in a reboot loop does
// not resolve and re-fire on every cycle.
func TestOfflineRequiresSustainedRecovery(t *testing.T) {
	r := ruleFor(t, AlertOffline)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if e := st.advance(r, r.observe(false, 0, true), now); e == nil {
		t.Fatal("offline should fire immediately")
	}
	now = now.Add(15 * time.Second)
	if e := st.advance(r, r.observe(true, 0, false), now); e != nil {
		t.Fatalf("a single online sample must not resolve, got %+v", e)
	}
	now = now.Add(60 * time.Second)
	if e := st.advance(r, r.observe(true, 0, false), now); e == nil || e.To != AlertStateOK {
		t.Fatalf("sustained recovery should resolve, got %+v", e)
	}
}

// A resolve reports how long the episode lasted, which is what lets the UI
// distinguish a node that just failed from one down for days.
func TestResolveReportsEpisodeDuration(t *testing.T) {
	r := ruleFor(t, AlertOffline)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	st.advance(r, r.observe(false, 0, true), now)
	now = now.Add(3 * time.Hour)
	st.advance(r, r.observe(true, 0, false), now)
	now = now.Add(61 * time.Second)
	e := st.advance(r, r.observe(true, 0, false), now)
	if e == nil || e.To != AlertStateOK {
		t.Fatalf("expected resolve, got %+v", e)
	}
	if e.Duration < 3*time.Hour {
		t.Fatalf("expected duration >= 3h, got %s", e.Duration)
	}
}

// An online node whose Xray is dead must raise an alert; the same node offline
// must not, because offline already says it.
func TestXrayDownOnlyWhileReporting(t *testing.T) {
	r := ruleFor(t, AlertXrayDown)
	n := nodeReading{xrayOK: false}

	if _, flag := ruleInput(r, n, true); !flag {
		t.Fatal("an online node with xray down must breach")
	}
	if _, flag := ruleInput(r, n, false); flag {
		t.Fatal("an offline node must not raise xray_down on top of offline")
	}
}

// A frozen episode that resumes is the same episode, so it must not fire again.
// This was a real defect: only State==firing suppressed the edge, so a node
// flapping with a full disk re-notified on every reconnect.
func TestStaleResumptionDoesNotRefire(t *testing.T) {
	r := ruleFor(t, AlertDisk)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	e := st.advance(r, r.observe(true, 92, false), now)
	if e == nil || e.To != AlertStateFiring {
		t.Fatalf("setup: expected the first breach to fire, got %+v", e)
	}
	firedAt := *st.FiredAt

	// Three offline/online round trips, each still breaching on return.
	for i := 0; i < 3; i++ {
		now = now.Add(30 * time.Second)
		if e := st.advance(r, r.observe(false, 92, false), now); e != nil {
			t.Fatalf("round %d: going offline emitted %+v", i, e)
		}
		if st.State != AlertStateStale {
			t.Fatalf("round %d: expected stale, got %q", i, st.State)
		}
		now = now.Add(30 * time.Second)
		if e := st.advance(r, r.observe(true, 92, false), now); e != nil {
			t.Fatalf("round %d: resuming re-fired with %+v", i, e)
		}
		if st.State != AlertStateFiring {
			t.Fatalf("round %d: expected firing on resume, got %q", i, st.State)
		}
		if !st.FiredAt.Equal(firedAt) {
			t.Fatalf("round %d: fired_at moved to %v, want %v", i, st.FiredAt, firedAt)
		}
	}
}

// A breach that never lasted long enough to fire must not be frozen as an open
// alert, and must not later emit a recovery for something nobody was told about.
func TestPendingBreachDoesNotSurviveGoingOffline(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	st.advance(r, r.observe(true, 92, false), now)
	if st.State != AlertStatePending {
		t.Fatalf("setup: expected pending, got %q", st.State)
	}

	now = now.Add(30 * time.Second)
	if e := st.advance(r, r.observe(false, 92, false), now); e != nil {
		t.Fatalf("going offline emitted %+v", e)
	}
	if st.State != AlertStateOK {
		t.Fatalf("a never-fired breach must return to ok, got %q", st.State)
	}

	// Come back healthy and hold: there is nothing to recover from, so no edge.
	now = now.Add(10 * time.Minute)
	for i := 0; i < 40; i++ {
		if e := st.advance(r, r.observe(true, 20, false), now); e != nil {
			t.Fatalf("recovery emitted %+v for an alert that never fired", e)
		}
		now = now.Add(15 * time.Second)
	}
}

// A resolved episode must not leak its age into the next one.
func TestNewEpisodeStartsItsOwnClock(t *testing.T) {
	r := ruleFor(t, AlertOffline)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	st.advance(r, r.observe(false, 0, true), now)
	firstFired := *st.FiredAt

	now = now.Add(2 * time.Hour)
	st.advance(r, r.observe(true, 0, false), now)
	now = now.Add(61 * time.Second)
	if e := st.advance(r, r.observe(true, 0, false), now); e == nil || e.To != AlertStateOK {
		t.Fatalf("expected resolve, got %+v", e)
	}
	if st.FiredAt != nil {
		t.Fatalf("resolve must clear fired_at, got %v", st.FiredAt)
	}

	now = now.Add(24 * time.Hour)
	e := st.advance(r, r.observe(false, 0, true), now)
	if e == nil || e.To != AlertStateFiring {
		t.Fatalf("expected a new episode to fire, got %+v", e)
	}
	if st.FiredAt == nil || st.FiredAt.Equal(firstFired) {
		t.Fatalf("new episode reused the old fired_at %v", st.FiredAt)
	}
	if !st.FiredAt.Equal(now) {
		t.Fatalf("new episode should be dated now, got %v want %v", st.FiredAt, now)
	}
}

// Once resolved, further healthy samples must not keep rewriting resolved_at.
func TestResolvedAtIsStampedOnceOnTheEdge(t *testing.T) {
	r := ruleFor(t, AlertOffline)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	st.advance(r, r.observe(false, 0, true), now)
	now = now.Add(5 * time.Minute)
	st.advance(r, r.observe(true, 0, false), now)
	now = now.Add(61 * time.Second)
	st.advance(r, r.observe(true, 0, false), now)
	if st.ResolvedAt == nil {
		t.Fatal("expected resolved_at on the resolve edge")
	}
	stamped := *st.ResolvedAt

	for i := 0; i < 20; i++ {
		now = now.Add(15 * time.Second)
		if e := st.advance(r, r.observe(true, 0, false), now); e != nil {
			t.Fatalf("a healthy sample after resolve emitted %+v", e)
		}
	}
	if !st.ResolvedAt.Equal(stamped) {
		t.Fatalf("resolved_at moved to %v, want it fixed at %v", st.ResolvedAt, stamped)
	}
}

// An escalation happens inside an episode that is already firing, so it must
// report the tier change while leaving fired_at where it was: the age is how long
// the condition has been true, not how long it has been severe.
func TestEscalationKeepsTheEpisodeAndItsAge(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 40; i++ {
		st.advance(r, r.observe(true, 82, false), now)
		now = now.Add(15 * time.Second)
	}
	if st.State != AlertStateFiring {
		t.Fatalf("setup: expected firing, got %q", st.State)
	}
	if st.Severity != SeverityWarning {
		t.Fatalf("setup: expected warning tier at 82, got %q", st.Severity)
	}
	firedAt := *st.FiredAt

	e := st.advance(r, r.observe(true, 96, false), now)
	if e == nil {
		t.Fatal("crossing the escalate threshold must emit an edge")
	}
	if !e.escalated() {
		t.Fatalf("expected an escalation edge, got %+v", e)
	}
	if e.SeverityFrom != SeverityWarning || e.SeverityTo != SeverityError {
		t.Fatalf("expected warning->error, got %q->%q", e.SeverityFrom, e.SeverityTo)
	}
	if !e.Notify {
		t.Fatal("an escalation on an existing row must notify")
	}
	if e.To != AlertStateFiring {
		t.Fatalf("an escalation stays firing, got %q", e.To)
	}
	if st.FiredAt == nil || !st.FiredAt.Equal(firedAt) {
		t.Fatalf("escalation moved fired_at to %v, want %v", st.FiredAt, firedAt)
	}

	// Holding at the higher tier is not a fresh escalation.
	now = now.Add(15 * time.Second)
	if e := st.advance(r, r.observe(true, 97, false), now); e != nil {
		t.Fatalf("staying escalated emitted %+v", e)
	}
}

// The escalation band damps a value hovering at the threshold, and a genuine
// de-escalation is silent: it is an improvement, not a recovery.
func TestDeescalationIsSilentAndBanded(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 40; i++ {
		st.advance(r, r.observe(true, 96, false), now)
		now = now.Add(15 * time.Second)
	}
	if st.Severity != SeverityError {
		t.Fatalf("setup: expected error tier at 96, got %q", st.Severity)
	}

	// 92 sits inside the 90..95 band, so the tier must hold rather than flip.
	for i := 0; i < 10; i++ {
		if e := st.advance(r, r.observe(true, 92, false), now); e != nil {
			t.Fatalf("a reading inside the escalation band emitted %+v", e)
		}
		if st.Severity != SeverityError {
			t.Fatalf("band reading dropped the tier to %q", st.Severity)
		}
		now = now.Add(15 * time.Second)
	}

	// Below the de-escalate threshold the tier drops, silently.
	if e := st.advance(r, r.observe(true, 85, false), now); e != nil {
		t.Fatalf("de-escalation must not emit an edge, got %+v", e)
	}
	if st.Severity != SeverityWarning {
		t.Fatalf("expected warning after dropping below deescalate, got %q", st.Severity)
	}
	if st.State != AlertStateFiring {
		t.Fatalf("de-escalation must not resolve the alert, got %q", st.State)
	}
}

// A first observation already above the escalate threshold must record the error
// tier without notifying, for the same reason any seed does not notify.
func TestSeedRecordsTheEscalatedTierWithoutNotifying(t *testing.T) {
	r := ruleFor(t, AlertDisk)
	st := &alertState{State: AlertStateOK} // never evaluated: exists == false
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	e := st.advance(r, r.observe(true, 99, false), now)
	if e == nil {
		t.Fatal("disk fires on first breach, so the seed must record an edge")
	}
	if e.Notify {
		t.Fatal("a seeded first observation must not notify, even at the error tier")
	}
	if st.Severity != SeverityError {
		t.Fatalf("expected the seed to land on the error tier, got %q", st.Severity)
	}
}

// A rule with no higher tier keeps its declared severity whatever the value is,
// so a boolean condition cannot be escalated by a stray reading.
func TestRulesWithoutATierNeverEscalate(t *testing.T) {
	r := ruleFor(t, AlertOffline)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if e := st.advance(r, r.observe(false, 0, true), now); e == nil || e.escalated() {
		t.Fatalf("offline should fire without escalating, got %+v", e)
	}
	if st.Severity != SeverityError {
		t.Fatalf("expected the rule's own severity, got %q", st.Severity)
	}
}

// A stale row is holding a frozen reading, so its tier must not move: a value
// nobody measured cannot justify an escalation, and recomputing one would page
// about a change that never happened.
func TestStaleFreezesTheSeverityTier(t *testing.T) {
	r := ruleFor(t, AlertCPU)
	st := seeded(AlertStateOK)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 40; i++ {
		st.advance(r, r.observe(true, 82, false), now)
		now = now.Add(15 * time.Second)
	}
	if st.Severity != SeverityWarning {
		t.Fatalf("setup: expected warning, got %q", st.Severity)
	}

	// The node stops reporting while its last sample was pinned high. The frozen
	// value must not be read as a fresh escalation.
	if e := st.advance(r, r.observe(false, 99, false), now); e != nil {
		t.Fatalf("freezing emitted %+v", e)
	}
	if st.State != AlertStateStale {
		t.Fatalf("expected stale, got %q", st.State)
	}
	if st.Severity != SeverityWarning {
		t.Fatalf("stale must keep the tier it was last measured at, got %q", st.Severity)
	}
}
