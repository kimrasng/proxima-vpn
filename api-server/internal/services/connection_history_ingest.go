package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type ConnectionHistoryBatch struct {
	SchemaVersion int                     `json:"schema_version"`
	BatchID       nodeprov.HistoryUUID    `json:"batch_id"`
	PayloadSHA256 string                  `json:"payload_sha256"`
	Events        []nodeprov.HistoryEvent `json:"events"`
}
type ConnectionHistoryReceipt struct {
	BatchID       nodeprov.HistoryUUID `json:"batch_id"`
	PayloadSHA256 string               `json:"payload_sha256"`
	Status        string               `json:"status"`
}

// DecodeConnectionHistoryBatch is the HTTP trust boundary. Shared strict event
// decoding rejects duplicate/missing fields, numeric U64 and extra wire members.
func DecodeConnectionHistoryBatch(raw []byte) (ConnectionHistoryBatch, error) {
	var batch ConnectionHistoryBatch
	if len(raw) > ConnectionHistoryMaxBatchBytes {
		return batch, historyError(413, "batch_too_large")
	}
	if _, err := nodeprov.CanonicalHistoryJSON(raw); err != nil {
		return batch, historyError(400, "invalid_json")
	}
	var envelope struct {
		SchemaVersion int                  `json:"schema_version"`
		BatchID       nodeprov.HistoryUUID `json:"batch_id"`
		PayloadSHA256 string               `json:"payload_sha256"`
		Events        []json.RawMessage    `json:"events"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&envelope); err != nil {
		return batch, historyError(400, "invalid_batch")
	}
	if envelope.SchemaVersion != 1 || envelope.BatchID.Validate() != nil || len(envelope.Events) == 0 || len(envelope.Events) > ConnectionHistoryMaxBatchEvents {
		return batch, historyError(400, "invalid_batch")
	}
	batch = ConnectionHistoryBatch{SchemaVersion: 1, BatchID: envelope.BatchID, PayloadSHA256: envelope.PayloadSHA256, Events: make([]nodeprov.HistoryEvent, 0, len(envelope.Events))}
	for _, line := range envelope.Events {
		var err error
		line, err = nodeprov.CanonicalHistoryJSON(bytes.TrimSpace(line))
		if err != nil {
			return batch, historyError(400, "invalid_event")
		}
		event, err := nodeprov.DecodeHistoryEvent(append(line, '\n'))
		if err != nil {
			return batch, historyError(400, "invalid_event")
		}
		batch.Events = append(batch.Events, event)
	}
	digest, err := nodeprov.HistoryEventsDigest(batch.Events)
	if err != nil {
		return batch, err
	}
	if digest != batch.PayloadSHA256 {
		return batch, historyError(400, "digest_mismatch")
	}
	return batch, nil
}

// Ingest serializes a node's uploads with its control row. Receipts, immutable
// events, projections and gaps commit together before a success ACK is returned.
func (s *ConnectionHistoryService) Ingest(ctx context.Context, node string, batch ConnectionHistoryBatch) (ConnectionHistoryReceipt, error) {
	ack := ConnectionHistoryReceipt{BatchID: batch.BatchID, PayloadSHA256: batch.PayloadSHA256, Status: "committed"}
	// Direct integration callers cross the same boundary as HTTP.
	raw, err := json.Marshal(batch)
	if err != nil {
		return ack, err
	}
	batch, err = DecodeConnectionHistoryBatch(raw)
	if err != nil {
		return ack, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ack, err
	}
	defer tx.Rollback(ctx)
	if err = ensureHistoryControl(ctx, tx, node); err != nil {
		return ack, err
	}
	if _, err = readHistoryConfig(ctx, tx, node); err != nil {
		return ack, err
	}
	var priorHash, priorStatus string
	err = tx.QueryRow(ctx, `SELECT payload_sha256,status FROM connection_log_batches WHERE node_origin=$1 AND batch_id=$2`, node, string(batch.BatchID)).Scan(&priorHash, &priorStatus)
	if err == nil {
		if priorHash != batch.PayloadSHA256 {
			return ack, historyError(409, "batch_conflict")
		}
		ack.Status = priorStatus
		return ack, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ack, err
	}
	now := s.now().UTC()
	active := 0
	for _, e := range batch.Events {
		expired, eerr := s.acceptHistoryEvent(ctx, tx, node, e, now)
		if eerr != nil {
			return ack, eerr
		}
		if !expired {
			active++
		}
	}
	if active == 0 {
		ack.Status = "expired"
	}
	_, err = tx.Exec(ctx, `INSERT INTO connection_log_batches VALUES($1,$2,$3,$4,$5)`, node, string(batch.BatchID), batch.PayloadSHA256, ack.Status, now)
	if err != nil {
		return ack, err
	}
	return ack, tx.Commit(ctx)
}
