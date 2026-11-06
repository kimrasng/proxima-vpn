package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"time"
)

func (s *ConnectionHistoryService) acceptHistoryEvent(ctx context.Context, tx pgx.Tx, node string, e nodeprov.HistoryEvent, now time.Time) (bool, error) {
	line, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	canonical, err := nodeprov.CanonicalHistoryJSON(line)
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(canonical)
	digest := hex.EncodeToString(hash[:])
	var old string
	var oldExpired bool
	err = tx.QueryRow(ctx, `SELECT payload_sha256,expired FROM connection_log_event_receipts WHERE node_origin=$1 AND event_id=$2`, node, e.EventID).Scan(&old, &oldExpired)
	if err == nil {
		if old != digest {
			return false, historyError(409, "event_conflict")
		}
		return oldExpired, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var issued time.Time
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT issued_at,enabled FROM connection_log_revisions WHERE node_origin=$1 AND revision=$2 AND epoch=$3`, node, e.ConfigRevision.String(), string(e.CollectionEpoch)).Scan(&issued, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, historyError(409, "epoch_or_revision_unissued")
	}
	if err != nil {
		return false, err
	}
	if !enabled && (e.Boundary == nil || e.Boundary.Action != "disabled") {
		return false, historyError(409, "revision_disabled")
	}
	if e.Boundary != nil && ((e.Boundary.Action == "disabled") == enabled) {
		return false, historyError(409, "boundary_revision_conflict")
	}
	if historyIsEntity(e.Kind) {
		var barriers []byte
		if err = tx.QueryRow(ctx, `SELECT barriers FROM connection_log_epochs WHERE node_origin=$1 AND epoch=$2`, node, string(e.CollectionEpoch)).Scan(&barriers); err != nil {
			return false, err
		}
		var intervals map[string]nodeprov.HistoryEvent
		if err = json.Unmarshal(barriers, &intervals); err != nil {
			return false, err
		}
		for _, boundary := range intervals {
			if boundary.ProducerID != e.ProducerID || boundary.ObserverBootID != e.ObserverBootID {
				continue
			}
			if (boundary.Boundary.Action == "enabled" && e.Sequence <= boundary.Sequence) ||
				(boundary.Boundary.Action == "disabled" && e.Sequence >= boundary.Sequence) {
				return false, historyError(409, "outside_applied_interval")
			}
		}
	}
	if e.Auth.CredentialRef != nil {
		var manifest []byte
		err = tx.QueryRow(ctx, `SELECT manifest FROM connection_log_bindings WHERE node_origin=$1 AND credential_ref=$2 AND binding_revision=$3`, node, string(*e.Auth.CredentialRef), e.Auth.BindingRevision.String()).Scan(&manifest)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, historyError(409, "binding_unissued")
		}
		if err != nil {
			return false, err
		}
		var b nodeprov.HistoryBinding
		if err = json.Unmarshal(manifest, &b); err != nil {
			return false, err
		}
		if b.Kind != e.Auth.Kind || b.Runtime != e.Runtime || e.InboundTag == nil || b.InboundTag != *e.InboundTag || b.BindingRevision > e.ConfigRevision {
			return false, historyError(409, "binding_mismatch")
		}
	}
	eventTime, err := time.Parse(time.RFC3339Nano, string(e.EventTime))
	if err != nil {
		return false, err
	}
	// Wall clock alone cannot authorize a new boot in an old epoch. Fresh node
	// status registers the producer; a recent issued revision also bootstraps it.
	var fresh bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connection_log_producers WHERE node_origin=$1 AND epoch=$2 AND producer_id=$3 AND boot_id=$4 AND authenticated_at >= $5)`,
		node, string(e.CollectionEpoch), e.ProducerID, string(e.ObserverBootID), now.Add(-ConnectionHistoryReplayAge)).Scan(&fresh)
	if err != nil {
		return false, err
	}
	expired := eventTime.Before(now.Add(-ConnectionHistoryReplayAge))
	if !fresh && issued.Before(now.Add(-ConnectionHistoryReplayAge)) && !expired {
		return false, historyError(409, "producer_receipt_stale")
	}
	if eventTime.After(now.Add(5 * time.Minute)) {
		return false, historyError(400, "event_time_future")
	}
	_, err = tx.Exec(ctx, `INSERT INTO connection_log_event_receipts(node_origin,event_id,payload_sha256,received_at,expired) VALUES($1,$2,$3,$4,$5)`, node, e.EventID, digest, now, expired)
	if err != nil {
		return false, err
	}
	if expired {
		return true, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO connection_log_producers VALUES($1,$2,$3,$4,$5) ON CONFLICT(node_origin,epoch,producer_id,boot_id) DO UPDATE SET authenticated_at=EXCLUDED.authenticated_at`,
		node, string(e.CollectionEpoch), e.ProducerID, string(e.ObserverBootID), now)
	if err != nil {
		return false, err
	}
	if err = database.EnsureConnectionHistoryPartition(ctx, tx, now); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO connection_log_events VALUES($1,$2,$3,$4)`, node, e.EventID, now, line); err != nil {
		return false, err
	}
	switch e.Kind {
	case "collection_gap":
		if _, err = tx.Exec(ctx, `INSERT INTO connection_log_gaps VALUES($1,$2,$3,$4,$5)`, node, e.EventID, string(e.CollectionEpoch), now, line); err != nil {
			return false, err
		}
		for _, table := range []string{"connection_flows", "connection_sessions"} {
			_, err = tx.Exec(ctx, `UPDATE `+table+` SET observation_status=CASE WHEN ended_at IS NULL THEN 'interrupted' ELSE observation_status END,
 payload=jsonb_set(jsonb_set(payload,'{bytes,final}','false'),'{quality,completeness}','"unknown"')
 WHERE node_origin=$1 AND epoch=$2 AND producer_id=$3 AND boot_id=$4`,
				node, string(e.CollectionEpoch), e.Gap.ProducerID, string(e.Gap.ObserverBootID))
			if err != nil {
				return false, err
			}
		}
	case "collection_boundary":
		if e.Boundary.Action == "enabled" || e.Boundary.Action == "disabled" {
			var conflict bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connection_log_events WHERE node_origin=$1 AND payload->>'collection_epoch'=$2
 AND payload->>'producer_id'=$3 AND payload->>'observer_boot_id'=$4 AND payload->>'kind' LIKE ANY(ARRAY['flow_%','tunnel_%'])
 AND (($6='enabled' AND (payload->>'sequence')::numeric <= $5::numeric) OR ($6='disabled' AND (payload->>'sequence')::numeric >= $5::numeric)))`,
				node, string(e.CollectionEpoch), e.ProducerID, string(e.ObserverBootID), e.Sequence.String(), e.Boundary.Action).Scan(&conflict)
			if err != nil {
				return false, err
			}
			if conflict {
				return false, historyError(409, "boundary_interval_conflict")
			}
		}
		key := e.ProducerID + "/" + string(e.ObserverBootID) + "/" + e.Boundary.Action
		_, err = tx.Exec(ctx, `UPDATE connection_log_epochs SET barriers=jsonb_set(barriers,ARRAY[$3],$4::jsonb,true) WHERE node_origin=$1 AND epoch=$2`, node, string(e.CollectionEpoch), key, line)
		if err != nil {
			return false, err
		}
	default:
		if err = projectHistoryEvent(ctx, tx, node, e, now); err != nil {
			return false, err
		}
	}
	return false, nil
}
