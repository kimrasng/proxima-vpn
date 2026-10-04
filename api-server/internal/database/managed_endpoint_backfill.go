package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

const legacyRealityName = "www.cloudflare.com"

type nodeSNIUpdate struct {
	name   *string
	source *string
	status reality.Status
	reason string
}

func backfillRealitySNI(ctx context.Context, conn *pgxpool.Conn) error {
	rows, err := conn.Query(ctx, `SELECT id::text FROM nodes ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan node id: %w", err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("list node ids: %w", err)
	}
	for _, id := range ids {
		if err := backfillNodeSNI(ctx, conn, id); err != nil {
			return fmt.Errorf("node %s: %w", id, err)
		}
	}
	return nil
}

func backfillNodeSNI(ctx context.Context, conn *pgxpool.Conn, id string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin node transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var canonical, source *string
	err = tx.QueryRow(ctx, `SELECT reality_client_sni, reality_sni_source FROM nodes WHERE id = $1 FOR UPDATE`, id).Scan(&canonical, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock node: %w", err)
	}
	listeners, err := nodeRealityListeners(ctx, tx, id)
	if err != nil {
		return err
	}
	state, err := evaluateNodeSNI(listeners, canonical, source)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET reality_client_sni = $2, reality_sni_source = $3, reality_sni_status = $4, reality_sni_error_code = NULLIF($5, '') WHERE id = $1`, id, state.name, state.source, state.status, state.reason); err != nil {
		return fmt.Errorf("persist SNI state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit SNI state: %w", err)
	}
	return nil
}

func nodeRealityListeners(ctx context.Context, tx pgx.Tx, id string) ([]reality.Listener, error) {
	rows, err := tx.Query(ctx, `SELECT protocol, tag, settings FROM inbounds WHERE node_id = $1 AND enabled = true ORDER BY created_at, id`, id)
	if err != nil {
		return nil, fmt.Errorf("read enabled inbounds: %w", err)
	}
	var listeners []reality.Listener
	var firstNames []string
	enabled := 0
	for rows.Next() {
		var protocol, tag string
		var settings json.RawMessage
		if err := rows.Scan(&protocol, &tag, &settings); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan enabled inbound: %w", err)
		}
		enabled++
		if protocol != "vless_reality" {
			continue
		}
		var configured struct {
			ServerNames []string `json:"server_names"`
		}
		if json.Unmarshal(settings, &configured) != nil || len(configured.ServerNames) == 0 {
			configured.ServerNames = []string{legacyRealityName}
		}
		if firstNames == nil {
			firstNames = configured.ServerNames
		}
		listeners = append(listeners, reality.Listener{
			Tag: tag, Enabled: true, Protocol: "vless", Security: "reality", ServerNames: configured.ServerNames,
		})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate enabled inbounds: %w", err)
	}
	if enabled == 0 {
		listeners = append(listeners, reality.Listener{
			Tag: "vless-reality", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{legacyRealityName},
		})
	}
	var tierEligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM devices d
		JOIN users u ON d.user_id = u.id
		JOIN plans p ON u.plan_id = p.id
		JOIN node_group_nodes ngn ON ngn.node_group_id = p.node_group_id
		WHERE ngn.node_id = $1 AND p.speed_limit > 0
		  AND u.is_active = true AND u.status = 'active'
		  AND (u.plan_expires_at IS NULL OR u.plan_expires_at > NOW())
		  AND (p.traffic_limit IS NULL OR u.traffic_used < p.traffic_limit)
		  AND (d.evicted_until IS NULL OR d.evicted_until <= NOW())
	)`, id).Scan(&tierEligible)
	if err != nil {
		return nil, fmt.Errorf("read speed-tier eligibility: %w", err)
	}
	if tierEligible {
		if firstNames == nil {
			firstNames = []string{legacyRealityName}
		}
		listeners = append(listeners, reality.Listener{
			Tag: "speed-tier", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: firstNames,
		})
	}
	return listeners, nil
}

func evaluateNodeSNI(listeners []reality.Listener, canonical, source *string) (nodeSNIUpdate, error) {
	backfill := "backfill"
	state := nodeSNIUpdate{name: canonical, source: &backfill}
	if source != nil {
		state.source = source
	}
	result, err := reality.Intersect(listeners)
	var invalid *reality.InvalidListenerNameError
	if errors.As(err, &invalid) {
		state.status, state.reason = reality.Conflict, "invalid_sni"
		return state, nil
	}
	if err != nil {
		return nodeSNIUpdate{}, fmt.Errorf("intersect Reality listeners: %w", err)
	}
	switch result.Status {
	case reality.NotApplicable:
		state.status = reality.NotApplicable
		if canonical == nil {
			state.source = nil
		}
	case reality.Conflict:
		state.status, state.reason = reality.Conflict, string(result.Reason)
	case reality.Valid:
		if canonical == nil {
			proposed := result.Proposed.String()
			state.name, state.status = &proposed, reality.Valid
			return state, nil
		}
		name, err := reality.NormalizeHostname(*canonical)
		if err != nil {
			state.status, state.reason = reality.Conflict, "invalid_sni"
		} else if !slices.Contains(result.Candidates, name) {
			state.status, state.reason = reality.Conflict, "listener_mismatch"
		} else {
			state.status = reality.Valid
		}
	default:
		return nodeSNIUpdate{}, fmt.Errorf("unexpected Reality intersection status %q", result.Status)
	}
	return state, nil
}
