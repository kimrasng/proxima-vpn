package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errInvalidStats  = errors.New("invalid traffic stats")
	errUnknownDevice = errors.New("unknown traffic device")
	errBatchConflict = errors.New("batch ID reused with different traffic")
)

// canonicalStats validates one immutable delivery and makes its digest
// independent of entry order. Each UUID is unique within a delivery.
func canonicalStats(stats []statEntry) (string, error) {
	ordered := append([]statEntry(nil), stats...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].XrayUUID < ordered[j].XrayUUID })
	for i, stat := range ordered {
		parsed, err := uuid.Parse(stat.XrayUUID)
		if err != nil || parsed.String() != stat.XrayUUID || stat.UpBytes < 0 || stat.DnBytes < 0 || stat.UpBytes > math.MaxInt64-stat.DnBytes {
			return "", errInvalidStats
		}
		if i > 0 && stat.XrayUUID == ordered[i-1].XrayUUID {
			return "", errInvalidStats
		}
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// accountStats commits the delivery marker and every traffic effect together.
// The unique key serializes retries, including retries after a lost response.
// Set-based INSERT and UPDATE keep database round trips constant per batch,
// rather than issuing three queries per device.
func accountStats(ctx context.Context, db *pgxpool.Pool, nodeID, batchID string, stats []statEntry) (bool, error) {
	if _, err := uuid.Parse(batchID); err != nil || len(stats) == 0 {
		return false, errInvalidStats
	}
	hash, err := canonicalStats(stats)
	if err != nil {
		return false, err
	}
	encoded, err := json.Marshal(stats)
	if err != nil {
		return false, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin traffic batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `INSERT INTO traffic_batches (node_id, batch_id, payload_hash)
		VALUES ($1, $2, $3) ON CONFLICT (node_id, batch_id) DO NOTHING`, nodeID, batchID, hash)
	if err != nil {
		return false, fmt.Errorf("claim traffic batch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var previous string
		if err := tx.QueryRow(ctx, `SELECT payload_hash FROM traffic_batches WHERE node_id=$1 AND batch_id=$2`, nodeID, batchID).Scan(&previous); err != nil {
			return false, fmt.Errorf("read existing traffic batch: %w", err)
		}
		if previous != hash {
			return false, errBatchConflict
		}
		return true, nil // The prior transaction already committed all effects.
	}

	var multiplier float64
	if err := tx.QueryRow(ctx, `SELECT traffic_multiplier FROM nodes WHERE id=$1`, nodeID).Scan(&multiplier); err != nil || multiplier <= 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return false, fmt.Errorf("read valid node traffic multiplier: %v", err)
	}

	// INSERT's FK check protects each mapped device until commit. If a device
	// vanished, fewer rows are inserted, and rolling back drops all effects.
	tag, err = tx.Exec(ctx, `INSERT INTO traffic_logs (device_id, node_id, up_bytes, dn_bytes)
		SELECT d.id, $1, i.up_bytes, i.dn_bytes
		FROM jsonb_to_recordset($2::jsonb) AS i(xray_uuid text, up_bytes bigint, dn_bytes bigint)
		JOIN devices d ON d.xray_uuid = i.xray_uuid`, nodeID, string(encoded))
	if err != nil {
		return false, fmt.Errorf("record traffic: %w", err)
	}
	if tag.RowsAffected() != int64(len(stats)) {
		return false, errUnknownDevice
	}

	// Aggregate by user so a user with several devices is updated once. An
	// overflow or missing user aborts the entire transaction, including logs.
	tag, err = tx.Exec(ctx, `UPDATE users u SET traffic_used = u.traffic_used + charges.bytes
		FROM (SELECT d.user_id,
		             SUM(ROUND((i.up_bytes::numeric + i.dn_bytes::numeric) * $2::numeric))::bigint AS bytes
		      FROM jsonb_to_recordset($1::jsonb) AS i(xray_uuid text, up_bytes bigint, dn_bytes bigint)
		      JOIN devices d ON d.xray_uuid = i.xray_uuid
		      GROUP BY d.user_id) charges
		WHERE u.id = charges.user_id`, string(encoded), multiplier)
	if err != nil {
		return false, fmt.Errorf("charge traffic: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, errors.New("traffic batch had no chargeable user")
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit traffic batch: %w", err)
	}
	return false, nil
}
