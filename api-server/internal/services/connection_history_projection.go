package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func historyCounterRegressed(older, newer *nodeprov.HistoryU64) bool {
	return older != nil && newer != nil && *older > *newer
}

func projectHistoryEvent(ctx context.Context, tx pgx.Tx, node string, e nodeprov.HistoryEvent, now time.Time) error {
	table := "connection_sessions"
	id := e.TunnelID
	flow := e.FlowID != nil
	if flow {
		table = "connection_flows"
		id = e.FlowID
	}
	// Compare both chronological neighbors, not only the current maximum. A
	// late middle snapshot can regress against its predecessor despite fitting
	// below the current high-water counter.
	if err := historyCheckNeighbors(ctx, tx, node, e, *id); err != nil {
		return err
	}
	var raw []byte
	var first time.Time
	var oldStatus string
	err := tx.QueryRow(ctx, `SELECT payload,first_observed_at,observation_status FROM `+table+` WHERE node_origin=$1 AND epoch=$2 AND entity_id=$3 FOR UPDATE`, node, string(e.CollectionEpoch), *id).Scan(&raw, &first, &oldStatus)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	current := e
	if exists {
		var old nodeprov.HistoryEvent
		if err = json.Unmarshal(raw, &old); err != nil {
			return err
		}
		oldAuth, _ := json.Marshal(old.Auth)
		newAuth, _ := json.Marshal(e.Auth)
		if old.ProducerID != e.ProducerID || old.ObserverBootID != e.ObserverBootID || string(oldAuth) != string(newAuth) ||
			!historySameString(old.TunnelID, e.TunnelID) || !historySameString(old.Protocol, e.Protocol) || old.Bytes.Layer != e.Bytes.Layer {
			return historyError(409, "entity_identity_conflict")
		}
		if e.Sequence == old.Sequence {
			return historyError(409, "entity_sequence_conflict")
		}
		older, newer := old, e
		if e.Sequence < old.Sequence {
			older, newer = e, old
			current = old
		}
		if historyCounterRegressed(older.Bytes.Upload, newer.Bytes.Upload) || historyCounterRegressed(older.Bytes.Download, newer.Bytes.Download) {
			return historyError(409, "counter_regression")
		}
		if older.Lifecycle.EndedAt != nil && newer.Lifecycle.EndedAt == nil {
			return historyError(409, "entity_reopened")
		}
		if older.Lifecycle.EndedAt != nil && newer.Lifecycle.EndedAt != nil {
			return historyError(409, "multiple_terminal_events")
		}
		if flow {
			if old.Destination.Transport != e.Destination.Transport || !historySameString(old.Destination.RequestedHost, e.Destination.RequestedHost) ||
				!historySameInt(old.Destination.RequestedPort, e.Destination.RequestedPort) {
				return historyError(409, "destination_conflict")
			}
		}
		// Fill missing start evidence without replacing the highest sequence or
		// immutable pagination timestamp. A late start never reopens an end.
		if current.Lifecycle.StartedAt == nil && older.Lifecycle.StartedAt != nil {
			current.Lifecycle.StartedAt = older.Lifecycle.StartedAt
			current.Lifecycle.PartialStart = false
			current.Quality.Start = older.Quality.Start
		}
	} else {
		first = now
	}
	status := "observing"
	var gap bool
	if current.Lifecycle.EndedAt != nil {
		status = "ended"
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connection_log_gaps WHERE node_origin=$1 AND epoch=$2 AND payload->'gap'->>'producer_id'=$3 AND payload->'gap'->>'observer_boot_id'=$4)`,
		node, string(e.CollectionEpoch), e.ProducerID, string(e.ObserverBootID)).Scan(&gap); err != nil {
		return err
	}
	if (gap || oldStatus == "interrupted") && current.Lifecycle.EndedAt == nil {
		status = "interrupted"
	}
	if gap || status == "interrupted" {
		current.Bytes.Final = false
		current.Quality.Completeness = "unknown"
	}
	payload, err := json.Marshal(current)
	if err != nil {
		return err
	}
	start, end := historyTimeValue(current.Lifecycle.StartedAt), historyTimeValue(current.Lifecycle.EndedAt)
	if !exists {
		cols := "node_origin,epoch,entity_id,producer_id,boot_id,sequence,first_observed_at,last_received_at,started_at,ended_at,observation_status,payload"
		values := "$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12"
		args := []any{node, string(e.CollectionEpoch), *id, e.ProducerID, string(e.ObserverBootID), current.Sequence.String(), first, now, start, end, status, payload}
		if flow {
			cols += ",layer,upload,download"
			values += ",$13,$14,$15"
			args = append(args, current.Bytes.Layer, historyCounterValue(current.Bytes.Upload), historyCounterValue(current.Bytes.Download))
		}
		_, err = tx.Exec(ctx, `INSERT INTO `+table+` (`+cols+`) VALUES (`+values+`)`, args...)
	} else {
		query := `UPDATE ` + table + ` SET sequence=$4,last_received_at=$5,started_at=$6,ended_at=$7,observation_status=$8,payload=$9`
		args := []any{node, string(e.CollectionEpoch), *id, current.Sequence.String(), now, start, end, status, payload}
		if flow {
			query += `,upload=$10,download=$11`
			args = append(args, historyCounterValue(current.Bytes.Upload), historyCounterValue(current.Bytes.Download))
		}
		_, err = tx.Exec(ctx, query+` WHERE node_origin=$1 AND epoch=$2 AND entity_id=$3`, args...)
	}
	return err
}
func historySameString(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func historySameInt(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func historyCounterValue(v *nodeprov.HistoryU64) any {
	if v == nil {
		return nil
	}
	return v.String()
}
func historyTimeValue(v *nodeprov.HistoryTime) any {
	if v == nil {
		return nil
	}
	return string(*v)
}

// An end is a terminal snapshot, never an inferred heartbeat/offline timeout.
func historyIsEntity(kind string) bool {
	return strings.HasPrefix(kind, "flow_") || strings.HasPrefix(kind, "tunnel_")
}

func historyCheckNeighbors(ctx context.Context, tx pgx.Tx, node string, e nodeprov.HistoryEvent, id string) error {
	for _, direction := range []struct{ op, order string }{{"<", "DESC"}, {">", "ASC"}} {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT payload FROM connection_log_events
 WHERE node_origin=$1 AND payload->>'collection_epoch'=$2 AND coalesce(payload->>'flow_id',payload->>'tunnel_id')=$3
 AND (payload->>'sequence')::numeric `+direction.op+` $4::numeric
 ORDER BY (payload->>'sequence')::numeric `+direction.order+` LIMIT 1`, node, string(e.CollectionEpoch), id, e.Sequence.String()).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var neighbor nodeprov.HistoryEvent
		if err = json.Unmarshal(raw, &neighbor); err != nil {
			return err
		}
		older, newer := neighbor, e
		if direction.op == ">" {
			older, newer = e, neighbor
		}
		if historyCounterRegressed(older.Bytes.Upload, newer.Bytes.Upload) || historyCounterRegressed(older.Bytes.Download, newer.Bytes.Download) {
			return historyError(409, "counter_regression")
		}
		if older.Lifecycle.EndedAt != nil {
			return historyError(409, "entity_reopened")
		}
	}
	return nil
}
