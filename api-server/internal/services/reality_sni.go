package services

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

type RealitySNIErrorKind string

const (
	RealitySNIMalformed        RealitySNIErrorKind = "malformed_canonical"
	RealitySNINoListener       RealitySNIErrorKind = "no_reality_listener"
	RealitySNIListenerConflict RealitySNIErrorKind = "listener_conflict"
	RealitySNIInvalidListener  RealitySNIErrorKind = "invalid_listener_name"
	RealitySNIMismatch         RealitySNIErrorKind = "canonical_listener_mismatch"
	RealitySNIMissingNode      RealitySNIErrorKind = "missing_node"
	RealitySNIEntryUnavailable RealitySNIErrorKind = "missing_entry_hostname"
	RealitySNILockMismatch     RealitySNIErrorKind = "lock_mismatch"
)

type RealitySNIError struct {
	Kind  RealitySNIErrorKind
	Cause error
}

func (e *RealitySNIError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("Reality SNI %s: %v", e.Kind, e.Cause)
	}
	return "Reality SNI " + string(e.Kind)
}

func (e *RealitySNIError) Unwrap() error { return e.Cause }

// LockedRealityNode is issued only after SELECT FOR UPDATE in the caller's transaction.
// It is invalid after that transaction ends; callers must roll back on any failure.
type LockedRealityNode struct {
	id        string
	tx        pgx.Tx
	canonical *string
	source    string
}

type ProspectiveRealityListeners struct{ Listeners []reality.Listener }

func LockRealityNode(ctx context.Context, tx pgx.Tx, nodeID string) (*LockedRealityNode, error) {
	var id string
	var name, source *string
	err := tx.QueryRow(ctx, `SELECT id::text, reality_client_sni, reality_sni_source FROM nodes WHERE id = $1 FOR UPDATE`, nodeID).Scan(&id, &name, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &RealitySNIError{Kind: RealitySNIMissingNode}
	}
	if err != nil {
		return nil, fmt.Errorf("lock Reality node: %w", err)
	}
	locked := &LockedRealityNode{id: id, tx: tx, canonical: name}
	if source != nil {
		locked.source = *source
	}
	return locked, nil
}

func (locked *LockedRealityNode) check(tx pgx.Tx) error {
	if locked == nil || locked.tx == nil || locked.tx != tx || locked.id == "" {
		return &RealitySNIError{Kind: RealitySNILockMismatch}
	}
	return nil
}

func UpdateCanonicalRealitySNI(ctx context.Context, tx pgx.Tx, locked *LockedRealityNode, raw string) error {
	if err := locked.check(tx); err != nil {
		return err
	}
	name, err := reality.NormalizeHostname(raw)
	if err != nil {
		return &RealitySNIError{Kind: RealitySNIMalformed, Cause: err}
	}
	listeners, err := LoadEffectiveRealityListeners(ctx, tx, locked)
	if err != nil {
		return err
	}
	result, err := intersectServiceListeners(listeners)
	if err != nil {
		return err
	}
	if !slices.Contains(result.Candidates, name) {
		return &RealitySNIError{Kind: RealitySNIMismatch}
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET reality_client_sni=$2, reality_sni_source='admin', reality_sni_status='valid', reality_sni_error_code=NULL WHERE id=$1`, locked.id, name.String()); err != nil {
		return fmt.Errorf("persist admin Reality SNI: %w", err)
	}
	stored := name.String()
	locked.canonical, locked.source = &stored, "admin"
	return nil
}

type prospectiveSNIState struct{ name, source string }

func evaluateProspectiveSNI(proposed ProspectiveRealityListeners, canonical *string, source string) (prospectiveSNIState, error) {
	result, err := intersectServiceListeners(proposed)
	if err != nil {
		return prospectiveSNIState{}, err
	}
	if canonical == nil {
		return prospectiveSNIState{name: result.Proposed.String(), source: "backfill"}, nil
	}
	name, err := reality.NormalizeHostname(*canonical)
	if err != nil {
		return prospectiveSNIState{}, &RealitySNIError{Kind: RealitySNIMalformed, Cause: err}
	}
	if !slices.Contains(result.Candidates, name) {
		return prospectiveSNIState{}, &RealitySNIError{Kind: RealitySNIMismatch}
	}
	return prospectiveSNIState{name: *canonical, source: source}, nil
}

func intersectServiceListeners(listeners ProspectiveRealityListeners) (reality.Result, error) {
	result, err := reality.Intersect(listeners.Listeners)
	var invalid *reality.InvalidListenerNameError
	if errors.As(err, &invalid) {
		return reality.Result{}, &RealitySNIError{Kind: RealitySNIInvalidListener, Cause: err}
	}
	if err != nil {
		return reality.Result{}, fmt.Errorf("intersect Reality listeners: %w", err)
	}
	switch result.Status {
	case reality.Valid:
		return result, nil
	case reality.NotApplicable:
		return reality.Result{}, &RealitySNIError{Kind: RealitySNINoListener}
	case reality.Conflict:
		return reality.Result{}, &RealitySNIError{Kind: RealitySNIListenerConflict}
	default:
		return reality.Result{}, fmt.Errorf("unexpected Reality listener status %q", result.Status)
	}
}

func ValidateProspectiveRealityListeners(ctx context.Context, tx pgx.Tx, locked *LockedRealityNode, proposed ProspectiveRealityListeners) error {
	if err := locked.check(tx); err != nil {
		return err
	}
	state, err := evaluateProspectiveSNI(proposed, locked.canonical, locked.source)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET reality_client_sni=$2, reality_sni_source=$3, reality_sni_status='valid', reality_sni_error_code=NULL WHERE id=$1`, locked.id, state.name, state.source); err != nil {
		return fmt.Errorf("persist repaired Reality SNI: %w", err)
	}
	locked.canonical, locked.source = &state.name, state.source
	return nil
}

func MarkRealitySNINotApplicable(ctx context.Context, tx pgx.Tx, locked *LockedRealityNode) error {
	if err := locked.check(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET reality_sni_status='not_applicable', reality_sni_error_code=NULL WHERE id=$1`, locked.id); err != nil {
		return fmt.Errorf("persist not-applicable Reality SNI: %w", err)
	}
	return nil
}

// LoadEffectiveRealityListeners mirrors WP-3 and the generator: enabled rows,
// legacy fallback only with zero enabled rows, then eligible speed-tier inbounds.
func LoadEffectiveRealityListeners(ctx context.Context, tx pgx.Tx, locked *LockedRealityNode) (ProspectiveRealityListeners, error) {
	if err := locked.check(tx); err != nil {
		return ProspectiveRealityListeners{}, err
	}
	rows, err := tx.Query(ctx, `SELECT protocol, tag, settings FROM inbounds WHERE node_id=$1 AND enabled=true ORDER BY created_at, id`, locked.id)
	if err != nil {
		return ProspectiveRealityListeners{}, fmt.Errorf("read enabled inbounds: %w", err)
	}
	var listeners []reality.Listener
	var firstNames []string
	enabled := 0
	for rows.Next() {
		var row inboundRow
		if err := rows.Scan(&row.Protocol, &row.Tag, &row.Settings); err != nil {
			rows.Close()
			return ProspectiveRealityListeners{}, fmt.Errorf("scan inbound: %w", err)
		}
		enabled++
		if row.Protocol != "vless_reality" {
			continue
		}
		_, names := realityParameters(row.Settings)
		if firstNames == nil {
			firstNames = names
		}
		listeners = append(listeners, reality.Listener{Tag: row.Tag, Enabled: true, Protocol: "vless", Security: "reality", ServerNames: names})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ProspectiveRealityListeners{}, fmt.Errorf("iterate inbounds: %w", err)
	}
	if enabled == 0 {
		listeners = append(listeners, reality.Listener{Tag: "vless-reality", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"www.cloudflare.com"}})
	}
	var tierEligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devices d JOIN users u ON d.user_id=u.id JOIN plans p ON u.plan_id=p.id JOIN node_group_nodes ngn ON ngn.node_group_id=p.node_group_id WHERE ngn.node_id=$1 AND p.speed_limit>0 AND u.is_active=true AND u.status='active' AND (u.plan_expires_at IS NULL OR u.plan_expires_at>NOW()) AND (p.traffic_limit IS NULL OR u.traffic_used<p.traffic_limit) AND (d.evicted_until IS NULL OR d.evicted_until<=NOW()))`, locked.id).Scan(&tierEligible)
	if err != nil {
		return ProspectiveRealityListeners{}, fmt.Errorf("read speed-tier eligibility: %w", err)
	}
	if tierEligible {
		if firstNames == nil {
			firstNames = []string{"www.cloudflare.com"}
		}
		listeners = append(listeners, reality.Listener{Tag: "speed-tier", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: firstNames})
	}
	return ProspectiveRealityListeners{Listeners: listeners}, nil
}
