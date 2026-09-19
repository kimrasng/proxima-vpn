package services

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Alert event types. EventNodeOffline already exists for the offline edge; the
// resolve side needed a counterpart, and the generic pair covers every other kind.
const (
	EventNodeOnline        = "node.online"
	EventNodeAlertFired    = "node.alert_fired"
	EventNodeAlertResolved = "node.alert_resolved"
	EventNodeAlertAcked    = "node.alert_acked"
	EventNodeAlertSilenced = "node.alert_silenced"
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
	Notify   bool
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
		SELECT n.id::text, n.name, n.status,
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
		if err := rows.Scan(&r.id, &r.name, &r.status, &r.cpu, &r.memory, &r.disk,
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

			if err := s.persist(ctx, n.id, rule, st, now); err != nil {
				return nil, err
			}
			if edge == nil {
				continue
			}
			transitions = append(transitions, AlertTransition{
				NodeID:   n.id,
				NodeName: n.name,
				Kind:     rule.kind,
				Severity: rule.severity,
				Value:    st.Value,
				To:       edge.To,
				Duration: edge.Duration,
				Notify:   edge.Notify,
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
		SELECT node_id::text, kind, state, value,
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
			nodeID, kind, state                       string
			value                                     float64
			breach, clear, fired, resolved, evaluated *time.Time
		)
		if err := rows.Scan(&nodeID, &kind, &state, &value,
			&breach, &clear, &fired, &resolved, &evaluated); err != nil {
			return nil, fmt.Errorf("scan alert state: %w", err)
		}
		out[alertKey{nodeID, AlertKind(kind)}] = &alertState{
			State: state, Value: value,
			BreachSince: breach, ClearSince: clear,
			FiredAt: fired, ResolvedAt: resolved, EvaluatedAt: evaluated,
			exists: true,
		}
	}
	return out, rows.Err()
}

func (s *AlertService) persist(ctx context.Context, nodeID string, r alertRule, st *alertState, now time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO node_alerts (node_id, kind, state, severity, value,
		                         breach_since, clear_since, fired_at, resolved_at, evaluated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (node_id, kind) DO UPDATE SET
			state = EXCLUDED.state,
			severity = EXCLUDED.severity,
			value = EXCLUDED.value,
			breach_since = EXCLUDED.breach_since,
			clear_since = EXCLUDED.clear_since,
			fired_at = EXCLUDED.fired_at,
			resolved_at = EXCLUDED.resolved_at,
			evaluated_at = EXCLUDED.evaluated_at,
			-- An acknowledgement belongs to the episode it was made against, so a
			-- resolve clears it and a later re-fire starts unacknowledged.
			acked_at = CASE WHEN EXCLUDED.state = 'ok' THEN NULL ELSE node_alerts.acked_at END,
			acked_by = CASE WHEN EXCLUDED.state = 'ok' THEN ''   ELSE node_alerts.acked_by END
	`, nodeID, string(r.kind), st.State, string(r.severity), st.Value,
		st.BreachSince, st.ClearSince, st.FiredAt, st.ResolvedAt, now)
	if err != nil {
		return fmt.Errorf("persist alert state for %s/%s: %w", nodeID, r.kind, err)
	}
	st.EvaluatedAt = &now
	return nil
}

// OpenAlert is one alert the operator should see: firing, or frozen stale.
type OpenAlert struct {
	ID            string
	NodeID        string
	NodeName      string
	Country       string
	Region        string
	NodeStatus    string
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
func (s *AlertService) ListOpen(ctx context.Context) ([]OpenAlert, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id::text, a.node_id::text, n.name, n.country, n.region, n.status,
		       a.kind, a.severity, a.state, a.value,
		       a.fired_at, a.acked_at, a.acked_by, a.silenced_until, a.evaluated_at
		FROM node_alerts a
		JOIN nodes n ON n.id = a.node_id
		WHERE a.state <> 'ok' AND n.status <> 'pending'
		ORDER BY
			CASE a.severity WHEN 'error' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
			n.name ASC, a.kind ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list open alerts: %w", err)
	}
	defer rows.Close()

	out := make([]OpenAlert, 0, 8)
	for rows.Next() {
		var a OpenAlert
		if err := rows.Scan(&a.ID, &a.NodeID, &a.NodeName, &a.Country, &a.Region, &a.NodeStatus,
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
		RETURNING a.node_id::text, a.kind,
		          (SELECT name FROM nodes WHERE id = a.node_id)
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
		RETURNING a.node_id::text, a.kind, a.silenced_until,
		          (SELECT name FROM nodes WHERE id = a.node_id)
	`, alertID, minutes).Scan(&nodeID, &kind, &until, &nodeName)
	if err != nil {
		if err == pgx.ErrNoRows {
			return OpenAlert{}, fmt.Errorf("alert %s not found", alertID)
		}
		return OpenAlert{}, fmt.Errorf("silence alert: %w", err)
	}
	return OpenAlert{ID: alertID, NodeID: nodeID, NodeName: nodeName, Kind: kind, SilencedUntil: until}, nil
}
