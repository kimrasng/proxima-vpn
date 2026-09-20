package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Alert event types. EventNodeOffline already exists for the offline edge; the
// resolve side needed a counterpart, and the generic pair covers every other kind.
const (
	EventNodeOnline         = "node.online"
	EventNodeAlertFired     = "node.alert_fired"
	EventNodeAlertResolved  = "node.alert_resolved"
	EventNodeAlertEscalated = "node.alert_escalated"
	EventNodeAlertAcked     = "node.alert_acked"
	EventNodeAlertSilenced  = "node.alert_silenced"
	EventNodeAlertClosed    = "node.alert_closed"
)

// AlertTransition is one boundary crossing the caller should record and notify.
type AlertTransition struct {
	NodeID   string
	NodeName string
	Kind     AlertKind
	Severity Severity
	Value    float64
	To       string
	Duration time.Duration
	// Escalated marks a severity rise inside an episode that keeps firing, which
	// is neither a new alert nor a recovery. SeverityFrom carries the tier it left
	// so the report can name both ends.
	Escalated    bool
	SeverityFrom Severity
	Notify       bool
}

// AlertService evaluates node conditions into persisted alert lifecycle state.
//
// There is deliberately no startup grace window. Restarting cannot re-page,
// because a condition that was already firing is read back as firing and crosses
// no boundary; a blanket window would only swallow alerts that genuinely started
// in the first minute after a deploy.
type AlertService struct {
	db *pgxpool.Pool
	// maxGap bounds how long an unobserved window may be before the
	// sustained-breach clocks reset.
	maxGap time.Duration
}

const alertMaxGap = 45 * time.Second

func NewAlertService(db *pgxpool.Pool) *AlertService {
	return &AlertService{db: db, maxGap: alertMaxGap}
}

type nodeReading struct {
	id        string
	name      string
	country   string
	region    string
	status    string
	cpu       float64
	memory    float64
	disk      float64
	xrayOK    bool
	shapingOK bool
}

// Evaluate runs one pass over every non-pending node and every rule, persists the
// resulting state, and returns only the transitions that crossed a boundary.
//
// Nodes still in 'pending' registration are excluded: they have never reported
// metrics, so their zeroed columns are absence of data rather than health.
func (s *AlertService) Evaluate(ctx context.Context) ([]AlertTransition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT n.id::text, n.name, n.country, n.region, n.status,
		       n.cpu_usage, n.memory_usage, n.disk_usage,
		       n.xray_running, COALESCE(n.shaping_ok, true)
		FROM nodes n
		WHERE n.status <> 'pending'
		ORDER BY n.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query nodes for alert evaluation: %w", err)
	}
	readings := make([]nodeReading, 0, 16)
	for rows.Next() {
		var r nodeReading
		if err := rows.Scan(&r.id, &r.name, &r.country, &r.region, &r.status,
			&r.cpu, &r.memory, &r.disk,
			&r.xrayOK, &r.shapingOK); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan node for alert evaluation: %w", err)
		}
		readings = append(readings, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan node for alert evaluation: %w", err)
	}

	existing, err := s.loadStates(ctx)
	if err != nil {
		return nil, err
	}

	// One server-side clock for the whole pass, so two rules on the same node
	// cannot disagree about when "now" was.
	var now time.Time
	if err := s.db.QueryRow(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		return nil, fmt.Errorf("read evaluation clock: %w", err)
	}

	transitions := make([]AlertTransition, 0, 8)

	for _, n := range readings {
		online := n.status == "online"
		for _, rule := range alertRules {
			// A node+kind with no row yet starts from the zero state, whose
			// exists=false marks the first observation as a seed rather than an
			// edge.
			st, ok := existing[alertKey{n.id, rule.kind}]
			if !ok {
				st = &alertState{State: AlertStateOK}
			}
			st.gapReset(now, s.maxGap)

			value, flag := ruleInput(rule, n, online)
			edge := st.advance(rule, rule.observe(online, value, flag), now)

			if err := s.persist(ctx, n, rule, st, now); err != nil {
				return nil, err
			}
			if edge == nil {
				continue
			}
			transitions = append(transitions, AlertTransition{
				NodeID:       n.id,
				NodeName:     n.name,
				Kind:         rule.kind,
				Severity:     st.Severity,
				Value:        st.Value,
				To:           edge.To,
				Duration:     edge.Duration,
				Escalated:    edge.escalated(),
				SeverityFrom: edge.SeverityFrom,
				Notify:       edge.Notify,
			})
		}
	}

	return transitions, nil
}

// ruleInput selects the reading a rule evaluates, returning a numeric value for
// threshold rules and a boolean for discrete ones.
func ruleInput(r alertRule, n nodeReading, online bool) (float64, bool) {
	switch r.kind {
	case AlertOffline:
		return 0, !online
	case AlertXrayDown:
		// Only meaningful while the node is reporting: an offline node's Xray is
		// down by definition, and saying so twice is noise.
		return 0, online && !n.xrayOK
	case AlertShapingFailed:
		return 0, !n.shapingOK
	case AlertCPU:
		return n.cpu, false
	case AlertMemory:
		return n.memory, false
	case AlertDisk:
		return n.disk, false
	}
	return 0, false
}

type alertKey struct {
	nodeID string
	kind   AlertKind
}

func (s *AlertService) loadStates(ctx context.Context) (map[alertKey]*alertState, error) {
	rows, err := s.db.Query(ctx, `
		SELECT node_id::text, kind, state, severity, value,
		       breach_since, clear_since, fired_at, resolved_at, evaluated_at
		FROM node_alerts
	`)
	if err != nil {
		return nil, fmt.Errorf("load alert state: %w", err)
	}
	defer rows.Close()

	out := map[alertKey]*alertState{}
	for rows.Next() {
		var (
			nodeID, kind, state, severity             string
			value                                     float64
			breach, clear, fired, resolved, evaluated *time.Time
		)
		if err := rows.Scan(&nodeID, &kind, &state, &severity, &value,
			&breach, &clear, &fired, &resolved, &evaluated); err != nil {
			return nil, fmt.Errorf("scan alert state: %w", err)
		}
		out[alertKey{nodeID, AlertKind(kind)}] = &alertState{
			State: state, Severity: Severity(severity), Value: value,
			BreachSince: breach, ClearSince: clear,
			FiredAt: fired, ResolvedAt: resolved, EvaluatedAt: evaluated,
			exists: true,
		}
	}
	return out, rows.Err()
}

func (s *AlertService) persist(ctx context.Context, n nodeReading, r alertRule, st *alertState, now time.Time) error {
	severity := st.Severity
	if severity == "" {
		severity = r.severity
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO node_alerts (node_id, node_name, node_country, node_region,
		                         kind, state, severity, value,
		                         breach_since, clear_since, fired_at, resolved_at, evaluated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (node_id, kind) DO UPDATE SET
			-- Refreshed every tick so a rename or a move is reflected while the
			-- node exists, and whatever was last true is what survives its
			-- deletion.
			node_name = EXCLUDED.node_name,
			node_country = EXCLUDED.node_country,
			node_region = EXCLUDED.node_region,
			state = EXCLUDED.state,
			severity = EXCLUDED.severity,
			value = EXCLUDED.value,
			breach_since = EXCLUDED.breach_since,
			clear_since = EXCLUDED.clear_since,
			fired_at = EXCLUDED.fired_at,
			resolved_at = EXCLUDED.resolved_at,
			evaluated_at = EXCLUDED.evaluated_at,
			-- An acknowledgement belongs to the episode it was made against, so a
			-- resolve clears it and a later re-fire starts unacknowledged. An
			-- escalation clears it for the same reason: the operator accepted a
			-- warning, not the error it has since become. Ranked against the stored
			-- row rather than a value carried in from the evaluator, so the
			-- comparison is against what was actually acknowledged. A de-escalation
			-- keeps the ack, because nothing new was asserted.
			acked_at = CASE
				WHEN EXCLUDED.state = 'ok' THEN NULL
				WHEN severity_rank(EXCLUDED.severity) > severity_rank(node_alerts.severity) THEN NULL
				ELSE node_alerts.acked_at END,
			acked_by = CASE
				WHEN EXCLUDED.state = 'ok' THEN ''
				WHEN severity_rank(EXCLUDED.severity) > severity_rank(node_alerts.severity) THEN ''
				ELSE node_alerts.acked_by END,
			-- Closure is audit, not state: Close resolves the row, so state='ok'
			-- already hides it. The stamp is cleared only when a new episode
			-- fires, so it never claims the current problem was the closed one.
			closed_at = CASE
				WHEN EXCLUDED.state = 'firing' THEN NULL
				ELSE node_alerts.closed_at END,
			closed_by = CASE
				WHEN EXCLUDED.state = 'firing' THEN ''
				ELSE node_alerts.closed_by END
	`, n.id, n.name, n.country, n.region,
		string(r.kind), st.State, string(severity), st.Value,
		st.BreachSince, st.ClearSince, st.FiredAt, st.ResolvedAt, now)
	if err != nil {
		return fmt.Errorf("persist alert state for %s/%s: %w", n.id, r.kind, err)
	}
	st.EvaluatedAt = &now
	return nil
}

// OpenAlert is one alert the operator should see: firing, or frozen stale.
type OpenAlert struct {
	ID       string
	NodeID   string
	NodeName string
	Country  string
	Region   string
	// NodeStatus is empty when the node no longer exists, which NodeDeleted
	// reports explicitly so a caller never has to infer it from a blank string.
	NodeStatus    string
	NodeDeleted   bool
	Kind          string
	Severity      string
	State         string
	Value         float64
	FiredAt       *time.Time
	AckedAt       *time.Time
	AckedBy       string
	SilencedUntil *time.Time
	EvaluatedAt   time.Time
}

// ListOpen returns the alerts that are not ok, newest problem first.
//
// Name and location come from the alert's own snapshot; the node is reached only
// for its live status, and with a LEFT JOIN, so an alert whose node is gone is
// still listed and still named.
func (s *AlertService) ListOpen(ctx context.Context) ([]OpenAlert, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id::text, a.node_id::text, a.node_name, a.node_country, a.node_region,
		       COALESCE(n.status, ''), n.id IS NULL,
		       a.kind, a.severity, a.state, a.value,
		       a.fired_at, a.acked_at, a.acked_by, a.silenced_until, a.evaluated_at
		FROM node_alerts a
		LEFT JOIN nodes n ON n.id = a.node_id
		WHERE a.state <> 'ok' AND COALESCE(n.status, '') <> 'pending'
		ORDER BY
			CASE a.severity WHEN 'error' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
			a.node_name ASC, a.kind ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list open alerts: %w", err)
	}
	defer rows.Close()

	out := make([]OpenAlert, 0, 8)
	for rows.Next() {
		var a OpenAlert
		if err := rows.Scan(&a.ID, &a.NodeID, &a.NodeName, &a.Country, &a.Region,
			&a.NodeStatus, &a.NodeDeleted,
			&a.Kind, &a.Severity, &a.State, &a.Value,
			&a.FiredAt, &a.AckedAt, &a.AckedBy, &a.SilencedUntil, &a.EvaluatedAt); err != nil {
			return nil, fmt.Errorf("scan open alert: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Silenced reports whether notifications for this node and kind are currently
// suppressed.
func (s *AlertService) Silenced(ctx context.Context, nodeID string, kind AlertKind) bool {
	var silenced bool
	err := s.db.QueryRow(ctx, `
		SELECT silenced_until IS NOT NULL AND silenced_until > NOW()
		FROM node_alerts WHERE node_id = $1 AND kind = $2
	`, nodeID, string(kind)).Scan(&silenced)
	if err != nil {
		return false
	}
	return silenced
}

// Acknowledge marks a firing alert as seen, which removes it from the notifiable
// count without hiding it.
func (s *AlertService) Acknowledge(ctx context.Context, alertID, by string, ack bool) (OpenAlert, error) {
	var (
		nodeID, kind string
		nodeName     string
	)
	err := s.db.QueryRow(ctx, `
		UPDATE node_alerts a SET
			acked_at = CASE WHEN $2 THEN NOW() ELSE NULL END,
			acked_by = CASE WHEN $2 THEN $3 ELSE '' END
		WHERE a.id = $1
		RETURNING a.node_id::text, a.kind, a.node_name
	`, alertID, ack, by).Scan(&nodeID, &kind, &nodeName)
	if err != nil {
		if err == pgx.ErrNoRows {
			return OpenAlert{}, fmt.Errorf("alert %s not found", alertID)
		}
		return OpenAlert{}, fmt.Errorf("acknowledge alert: %w", err)
	}
	return OpenAlert{ID: alertID, NodeID: nodeID, NodeName: nodeName, Kind: kind}, nil
}

// Silence suppresses notifications and the badge count for a node and kind until
// the window expires. Passing 0 clears it.
//
// Evaluation continues while silenced, so state and duration stay accurate: this
// hides the alarm, not the condition.
func (s *AlertService) Silence(ctx context.Context, alertID string, minutes int) (OpenAlert, error) {
	var (
		nodeID, kind, nodeName string
		until                  *time.Time
	)
	err := s.db.QueryRow(ctx, `
		UPDATE node_alerts a SET
			silenced_until = CASE WHEN $2 > 0 THEN NOW() + make_interval(mins => $2) ELSE NULL END
		WHERE a.id = $1
		RETURNING a.node_id::text, a.kind, a.silenced_until, a.node_name
	`, alertID, minutes).Scan(&nodeID, &kind, &until, &nodeName)
	if err != nil {
		if err == pgx.ErrNoRows {
			return OpenAlert{}, fmt.Errorf("alert %s not found", alertID)
		}
		return OpenAlert{}, fmt.Errorf("silence alert: %w", err)
	}
	return OpenAlert{ID: alertID, NodeID: nodeID, NodeName: nodeName, Kind: kind, SilencedUntil: until}, nil
}

// ErrAlertNotClosable rejects a closure the evaluator would immediately undo.
var ErrAlertNotClosable = errors.New(
	"only a stale alert, or one whose node no longer exists, can be closed")

// Close resolves an alert by hand, for the rows the evaluator can never resolve
// itself: leaving stale is gated on a fresh reading, so a node that is never
// coming back leaves its alert open forever.
//
// A firing alert on a live node is deliberately refused. Its condition is still
// measured as true, so the next tick would re-derive it, and clearing fired_at on
// the way would reset the episode's age. Acknowledging is the operation for "I am
// dealing with this".
//
// Eligibility is tested inside the statement, not by a preceding SELECT, so a node
// deleted or a reading arriving between check and write cannot slip a closure
// through - the UPDATE just matches no row.
func (s *AlertService) Close(ctx context.Context, alertID, by string) (OpenAlert, error) {
	var a OpenAlert
	err := s.db.QueryRow(ctx, `
		UPDATE node_alerts a SET
			state = 'ok',
			closed_at = NOW(),
			closed_by = $2,
			resolved_at = NOW(),
			-- The episode is over, so its markers go with it: a later breach must
			-- fire as new rather than resuming this one, and the acknowledgement
			-- and silence belonged to the episode being retired.
			fired_at = NULL,
			breach_since = NULL,
			clear_since = NULL,
			acked_at = NULL,
			acked_by = '',
			silenced_until = NULL
		WHERE a.id = $1
		  AND a.state <> 'ok'
		  AND (a.state = 'stale' OR NOT EXISTS (SELECT 1 FROM nodes n WHERE n.id = a.node_id))
		RETURNING a.node_id::text, a.kind, a.node_name, a.severity, a.value
	`, alertID, by).Scan(&a.NodeID, &a.Kind, &a.NodeName, &a.Severity, &a.Value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either the id does not exist or the row was not eligible. They are
			// reported apart because the first is a client mistake and the second
			// is a refusal the operator can act on.
			var exists bool
			if e := s.db.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM node_alerts WHERE id = $1)`, alertID,
			).Scan(&exists); e == nil && exists {
				return OpenAlert{}, ErrAlertNotClosable
			}
			return OpenAlert{}, fmt.Errorf("alert %s not found", alertID)
		}
		return OpenAlert{}, fmt.Errorf("close alert: %w", err)
	}
	a.ID = alertID
	a.State = AlertStateOK
	return a, nil
}
