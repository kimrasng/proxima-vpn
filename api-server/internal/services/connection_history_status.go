package services

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"slices"
	"time"
)

// RecordStatus is called only after node authentication (heartbeat integration).
// It does not trust state=collecting unless all currently enabled suppliers prove
// the issued revision. This is not evidence that an unreported runtime supports it.
func (s *ConnectionHistoryService) RecordStatus(ctx context.Context, node string, status ConnectionHistoryStatus) error {
	seen := make(map[string]bool)
	for _, p := range status.Producers {
		if p.ProducerID == "" || len(p.ProducerID) > 128 || seen[p.ProducerID] ||
			(p.ObserverBootID != nil && p.ObserverBootID.Validate() != nil) || len(p.RuntimeBuild) > 128 ||
			(p.Capabilities != nil && !nodeprov.HistoryCapabilitiesValid(p.Capabilities)) {
			return historyError(400, "invalid_producer")
		}
		seen[p.ProducerID] = true
	}
	if !slices.Contains([]string{"off", "pending", "collecting", "degraded", "unsupported", "error"}, status.State) {
		return historyError(400, "invalid_state")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = ensureHistoryControl(ctx, tx, node); err != nil {
		return err
	}
	cfg, err := readHistoryConfig(ctx, tx, node)
	if err != nil {
		return err
	}
	if status.DesiredRevision > cfg.Revision || status.AppliedRevision > cfg.Revision {
		return historyError(409, "revision_unissued")
	}
	if status.AppliedRevision > 0 {
		var epoch *string
		var enabled bool
		err = tx.QueryRow(ctx, `SELECT epoch::text,enabled FROM connection_log_revisions WHERE node_origin=$1 AND revision=$2`, node, status.AppliedRevision.String()).Scan(&epoch, &enabled)
		if err != nil {
			return historyError(409, "revision_unissued")
		}
		if enabled != status.Enabled || (epoch == nil) != (status.CollectionEpoch == nil) || (epoch != nil && *epoch != string(*status.CollectionEpoch)) {
			return historyError(409, "applied_conflict")
		}
	} else if status.Enabled || status.CollectionEpoch != nil {
		return historyError(409, "applied_conflict")
	}
	if status.State == "collecting" {
		if !cfg.Enabled || !status.Enabled || status.AppliedRevision != cfg.Revision || len(status.Producers) == 0 {
			return historyError(409, "not_collecting")
		}
		rows, e := tx.Query(ctx, `SELECT DISTINCT protocol FROM inbounds WHERE node_id=$1 AND enabled`, node)
		if e != nil {
			return e
		}
		count := 0
		for rows.Next() {
			var protocol string
			if e = rows.Scan(&protocol); e != nil {
				rows.Close()
				return e
			}
			count++
			ok := false
			for _, p := range status.Producers {
				if historyProducerRuntime(p) != historyProtocolRuntime(protocol) {
					continue
				}
				if p.AppliedRevision != cfg.Revision || !historyProducerSupported(p, protocol) {
					rows.Close()
					return historyError(409, "supplier_not_applied")
				}
				ok = true
			}
			if !ok {
				rows.Close()
				return historyError(409, "supplier_not_applied")
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if count == 0 {
			return historyError(409, "supplier_not_applied")
		}
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE connection_log_controls SET applied_revision=$2,applied_enabled=$3,state=$4,status=$5,status_at=$6,applied_at=$7 WHERE node_origin=$1`,
		node, status.AppliedRevision.String(), status.Enabled, status.State, raw, s.now(), status.AppliedAt)
	if err != nil {
		return err
	}
	if status.CollectionEpoch != nil {
		for _, p := range status.Producers {
			if p.ObserverBootID == nil {
				continue
			}
			_, err = tx.Exec(ctx, `INSERT INTO connection_log_producers VALUES($1,$2,$3,$4,$5) ON CONFLICT(node_origin,epoch,producer_id,boot_id) DO UPDATE SET authenticated_at=EXCLUDED.authenticated_at`, node, string(*status.CollectionEpoch), p.ProducerID, string(*p.ObserverBootID), s.now())
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// DecodeConnectionHistoryStatus consumes the manager's exact heartbeat member;
// no agent package import or shared heartbeat struct modification is needed.
func DecodeConnectionHistoryStatus(raw []byte) (ConnectionHistoryStatus, error) {
	var status ConnectionHistoryStatus
	if len(raw) > ConnectionHistoryMaxBatchBytes {
		return status, historyError(413, "status_too_large")
	}
	if _, err := nodeprov.CanonicalHistoryJSON(raw); err != nil {
		return status, historyError(400, "invalid_status")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&status); err != nil {
		return status, historyError(400, "invalid_status")
	}
	return status, nil
}

// ReadControl excludes the private binding manifest and derives offline from
// node heartbeat freshness rather than fabricating entity ends.
func (s *ConnectionHistoryService) ReadControl(ctx context.Context, node string) (map[string]any, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var retained bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connection_log_controls WHERE node_origin=$1)`, node).Scan(&retained); err != nil {
		return nil, err
	}
	if !retained {
		if err = ensureHistoryControl(ctx, tx, node); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	var enabled, applied, shared bool
	var rev, appliedRev, state, name string
	var epoch *string
	var statusAt, lastSeen, fetchAt, appliedAt *time.Time
	var raw []byte
	err = s.db.QueryRow(ctx, `SELECT c.enabled,c.revision::text,c.applied_revision::text,c.applied_enabled,c.collection_epoch::text,c.state,c.node_snapshot,c.status_at,n.last_seen,c.last_config_fetch_at,c.applied_at,c.status
 FROM connection_log_controls c LEFT JOIN nodes n ON n.id=c.node_id WHERE c.node_origin=$1`, node).Scan(&enabled, &rev, &appliedRev, &applied, &epoch, &state, &name, &statusAt, &lastSeen, &fetchAt, &appliedAt, &raw)
	if err != nil {
		return nil, err
	}
	if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connection_log_bindings WHERE node_origin=$1 AND revoked_at IS NULL AND manifest->>'kind'='shared')`, node).Scan(&shared); err != nil {
		return nil, err
	}
	if lastSeen == nil || lastSeen.Before(s.now().Add(-90*time.Second)) {
		state = "offline"
	}
	var status ConnectionHistoryStatus
	if err = json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	// Producer records contain no auth labels or binding manifests.
	return map[string]any{"node_id": node, "node_name": name, "enabled": enabled, "desired_revision": rev, "applied_revision": appliedRev, "applied_enabled": applied, "collection_epoch": epoch, "applied_collection_epoch": status.CollectionEpoch, "state": state, "status_at": statusAt, "last_config_fetch_at": fetchAt, "applied_at": appliedAt, "producers": status.Producers, "shared_identity": shared, "queue_bytes": status.QueueBytes, "dropped_events": status.DroppedEvents, "error_code": status.ErrorCode}, nil
}
