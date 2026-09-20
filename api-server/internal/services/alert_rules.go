package services

import "time"

// AlertKind identifies one alert condition. It is half of the (node_id, kind)
// identity that makes an alert row its own dedup key.
type AlertKind string

const (
	AlertOffline       AlertKind = "offline"
	AlertCPU           AlertKind = "cpu"
	AlertMemory        AlertKind = "memory"
	AlertDisk          AlertKind = "disk"
	AlertXrayDown      AlertKind = "xray_down"
	AlertShapingFailed AlertKind = "shaping_failed"
)

// Alert lifecycle states. `pending` is a breach that has not yet lasted long
// enough to fire; `stale` is a resource alert on a node that stopped reporting,
// whose readings are therefore frozen rather than current.
const (
	AlertStateOK      = "ok"
	AlertStatePending = "pending"
	AlertStateFiring  = "firing"
	AlertStateStale   = "stale"
)

// alertRule describes when one kind fires and when it clears.
//
// fire and clear are deliberately asymmetric: a single threshold makes a value
// hovering at the boundary flip the alert on every sample. The gap between them
// is the hysteresis band, and a reading inside it changes nothing.
//
// boolean rules (offline, xray, shaping) carry no numeric threshold and are
// evaluated from node state by the caller; their damping lives entirely in
// clearFor, because the condition itself is already discrete.
type alertRule struct {
	kind     AlertKind
	severity Severity
	fire     float64
	clear    float64
	fireFor  time.Duration
	clearFor time.Duration
	// escalate and deescalate are a second, higher band inside an episode that is
	// already firing, carrying severity to escalateSeverity while the value stays
	// above it. They need their own hysteresis for the same reason fire/clear do,
	// and they are left zero for a rule with no higher tier to reach.
	//
	// There is deliberately no escalateFor: an escalation can only happen inside
	// an episode that already sustained its breach for fireFor, so the spike
	// protection has already been paid for once.
	escalate         float64
	deescalate       float64
	escalateSeverity Severity
	// numeric distinguishes a threshold rule from a boolean one.
	numeric bool
	// resource marks a rule whose input comes from node-reported metrics, and
	// which therefore goes stale rather than resolving when a node stops
	// reporting.
	resource bool
}

// alertRules is the whole rule set. Order fixes the order alerts are reported in
// for one node.
//
// The durations are chosen so a human can predict them: 5 minutes is long enough
// that a single spiky sample cannot page anyone, and short enough that a real
// sustained problem is not hidden. Disk is exempt from a fire duration because it
// moves over hours, so waiting adds latency without preventing anything. Offline
// is exempt because node_monitor's staleness window already damps it; its
// damping belongs on the recovery side, where 60 seconds of heartbeats stop a
// node in a reboot loop from re-notifying every cycle.
//
// The escalation tiers mark where degraded turns into effectively unusable, which
// is why they are all error: a node pinned at 95% CPU is not serving traffic well,
// and a disk at 97% is close to the point where Xray cannot write its own logs.
// Memory and disk escalate later than CPU because their warning thresholds
// already start higher, so the same 15-point gap would put the tier past 100.
// offline, xray_down and shaping_failed have no tier: the first two are already
// error, and shaping_failed is a bounded degradation that never becomes an outage.
var alertRules = []alertRule{
	{kind: AlertOffline, severity: SeverityError, clearFor: 60 * time.Second},
	{kind: AlertXrayDown, severity: SeverityError, fireFor: 30 * time.Second, clearFor: 30 * time.Second, resource: true},
	{kind: AlertShapingFailed, severity: SeverityWarning, fireFor: 30 * time.Second, clearFor: 30 * time.Second, resource: true},
	{kind: AlertCPU, severity: SeverityWarning, fire: 80, clear: 70, escalate: 95, deescalate: 90, escalateSeverity: SeverityError, fireFor: 5 * time.Minute, clearFor: 5 * time.Minute, numeric: true, resource: true},
	{kind: AlertMemory, severity: SeverityWarning, fire: 85, clear: 75, escalate: 97, deescalate: 93, escalateSeverity: SeverityError, fireFor: 5 * time.Minute, clearFor: 5 * time.Minute, numeric: true, resource: true},
	{kind: AlertDisk, severity: SeverityWarning, fire: 90, clear: 85, escalate: 97, deescalate: 94, escalateSeverity: SeverityError, clearFor: 1 * time.Minute, numeric: true, resource: true},
}

// severityRank orders severities so an escalation can be told from a
// de-escalation. Only the two levels the rule table uses are ranked above the
// default; the rest share a floor because no rule escalates to them.
func severityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 2
	case SeverityWarning:
		return 1
	default:
		return 0
	}
}

// severityFor picks the tier a value belongs in, holding the current one while
// the reading sits inside the escalation band.
func (r alertRule) severityFor(current Severity, value float64) Severity {
	if r.escalateSeverity == "" {
		return r.severity
	}
	switch {
	case value >= r.escalate:
		return r.escalateSeverity
	case value < r.deescalate:
		return r.severity
	default:
		if current == "" {
			return r.severity
		}
		return current
	}
}

// alertState is the mutable part of one alert row.
type alertState struct {
	State string
	// Severity is the tier the current reading falls in, not the rule's static
	// level: an escalation mutates this in place, so persist writes it rather
	// than the rule's own severity.
	Severity    Severity
	Value       float64
	BreachSince *time.Time
	ClearSince  *time.Time
	FiredAt     *time.Time
	ResolvedAt  *time.Time
	EvaluatedAt *time.Time
	// exists is false for a node+kind that has never been evaluated. The first
	// observation of an already-breaching condition is a seed, not an edge, so it
	// must not notify.
	exists bool
}

// alertObservation is one rule's input for one node at one instant.
type alertObservation struct {
	// breaching and clearing are not complements: inside the hysteresis band
	// both are false, which is what holds the current state.
	breaching bool
	clearing  bool
	value     float64
	// reporting is false when the node is not online, which freezes resource
	// rules instead of resolving them.
	reporting bool
}

// alertEdge is a boundary crossing worth telling somebody about.
type alertEdge struct {
	To       string
	Duration time.Duration
	// SeverityFrom and SeverityTo are set only when the edge is a severity change
	// inside an episode that keeps firing. They let the caller report an
	// escalation as its own event rather than a second fire.
	SeverityFrom Severity
	SeverityTo   Severity
	// Notify is false for a seeded first observation: the condition was already
	// true before anything was watching, so reporting it as new is a lie and, on
	// first deploy, a notification burst.
	Notify bool
}

// escalated reports whether this edge is a mid-episode severity rise.
func (e *alertEdge) escalated() bool {
	return e.SeverityTo != "" && severityRank(e.SeverityTo) > severityRank(e.SeverityFrom)
}

// observe turns a rule plus a node's readings into that rule's input.
func (r alertRule) observe(online bool, value float64, flag bool) alertObservation {
	if r.numeric {
		return alertObservation{
			breaching: value >= r.fire,
			clearing:  value < r.clear,
			value:     value,
			reporting: online,
		}
	}
	// A boolean rule has no band to sit inside, so it is always exactly one of
	// breaching or clearing.
	return alertObservation{breaching: flag, clearing: !flag, value: value, reporting: online}
}

// gapReset discards a sustained-breach window that spans time nobody sampled.
//
// State survives a restart, which is the point - but if the evaluator was down
// for ten minutes, an old breach_since would let a five-minute condition be
// satisfied across a window in which nothing was observed. Treating the gap as
// unobserved restarts the clock instead.
func (s *alertState) gapReset(now time.Time, maxGap time.Duration) {
	if s.EvaluatedAt == nil || now.Sub(*s.EvaluatedAt) <= maxGap {
		return
	}
	s.BreachSince = nil
	s.ClearSince = nil
}

// advance applies one observation to the state and reports the edge it crossed,
// if any. It is pure: every time comes from the caller, never from the clock, so
// the state machine is fully testable and never reads a node-supplied timestamp.
func (s *alertState) advance(r alertRule, o alertObservation, now time.Time) *alertEdge {
	s.Value = o.value
	seeding := !s.exists
	s.exists = true

	// A resource rule on a node that stopped reporting is reading a frozen
	// value. Freeze the alert with it rather than asserting a recovery nobody
	// saw: resolving here would emit a false resolve and then re-fire, with a
	// second notification, the moment the node came back.
	if r.resource && !o.reporting {
		// Only an episode that actually fired is worth freezing. A breach still
		// short of its duration never became an alert, so carrying it into stale
		// would surface a problem nobody was ever told about - and later emit a
		// recovery for it.
		if s.FiredAt != nil {
			s.State = AlertStateStale
		} else {
			s.State = AlertStateOK
		}
		// The breach clock is discarded either way: time in which the node was
		// not reporting is time nobody observed. Severity is left alone: it
		// belongs to the frozen reading, and recomputing it from a stale value
		// would assert a tier change nobody measured.
		s.BreachSince = nil
		s.ClearSince = nil
		return nil
	}

	// Recomputed on every observed sample, including a seed, so a row always
	// carries the tier its value belongs in.
	prevSeverity := s.Severity
	s.Severity = r.severityFor(prevSeverity, o.value)

	switch {
	case o.breaching:
		s.ClearSince = nil
		if s.BreachSince == nil {
			s.BreachSince = &now
		}
		if s.State == AlertStateFiring {
			// The episode continues, so fired_at must not move - its age is how
			// long the condition has actually been true. A rise in tier is still
			// worth reporting, which a de-escalation is not: that is an
			// improvement, and paging about improvements trains people to ignore
			// the channel.
			if severityRank(s.Severity) > severityRank(prevSeverity) {
				return &alertEdge{
					To:           AlertStateFiring,
					SeverityFrom: prevSeverity,
					SeverityTo:   s.Severity,
					Notify:       !seeding,
				}
			}
			return nil
		}
		// An episode with FiredAt set never resolved - it was only frozen while
		// the node stopped reporting. Resuming it is not a new boundary, so it
		// restores firing immediately and notifies nobody a second time.
		if s.FiredAt != nil {
			s.State = AlertStateFiring
			return nil
		}
		if now.Sub(*s.BreachSince) >= r.fireFor {
			s.State = AlertStateFiring
			s.FiredAt = &now
			s.ResolvedAt = nil
			return &alertEdge{To: AlertStateFiring, Notify: !seeding}
		}
		s.State = AlertStatePending
		return nil

	case o.clearing:
		s.BreachSince = nil
		if s.ClearSince == nil {
			s.ClearSince = &now
		}
		// A resolve is only meaningful for an episode that fired. FiredAt, not the
		// state name, is what distinguishes one: a pending breach that clears has
		// nothing to recover from.
		fired := s.FiredAt != nil
		if now.Sub(*s.ClearSince) < r.clearFor {
			if s.State == AlertStatePending {
				s.State = AlertStateOK
			}
			return nil
		}
		if s.State == AlertStateOK {
			return nil
		}
		s.State = AlertStateOK
		if !fired {
			return nil
		}
		dur := now.Sub(*s.FiredAt)
		s.FiredAt = nil
		s.ResolvedAt = &now
		return &alertEdge{To: AlertStateOK, Duration: dur, Notify: !seeding}
	}

	// Inside the hysteresis band: neither timestamp advances, so a value
	// oscillating here cannot accumulate toward either boundary.
	return nil
}
