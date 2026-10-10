package scheduler

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

var ErrIncompleteEvictionFleet = errors.New("exit fleet is not ready for verified eviction")
var ErrPendingEviction = errors.New("another UUID eviction is pending")

// beginUUIDEviction writes the policy and a nonempty required Exit snapshot in
// one transaction. The agent closes existing TCP/XUDP associations on its next
// poll; a pending epoch must never be counted as a successful eviction.
func beginUUIDEviction(ctx context.Context, db *pgxpool.Pool, userID, target string, expected []string) (string, error) {
	if len(expected) == 0 {
		return "", ErrIncompleteEvictionFleet
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owner string
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, userID); err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM devices WHERE xray_uuid=$1 AND user_id=$2 AND (evicted_until IS NULL OR evicted_until<=NOW()) FOR UPDATE`, target, userID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPendingEviction
	}
	if err != nil {
		return "", fmt.Errorf("lock eviction target: %w", err)
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM uuid_evictions e JOIN devices d ON d.xray_uuid=e.device_uuid WHERE d.user_id=$1 AND e.status='pending')`, userID).Scan(&pending)
	if err != nil {
		return "", err
	}
	if pending {
		return "", ErrPendingEviction
	}
	// Recheck the complete fleet while holding this account's eviction lock;
	// a second scheduler or a changed Exit must not confirm a partial snapshot.
	var currentCount int
	err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM nodes WHERE role IN ('exit','both') AND status<>'pending'`).Scan(&currentCount)
	if err != nil || currentCount != len(expected) {
		return "", ErrIncompleteEvictionFleet
	}
	epoch := uuid.NewString()
	// Keep the credential denied until every required Exit confirms closure.
	// A timed cooldown starts only after confirmation, never while pending.
	_, err = tx.Exec(ctx, `UPDATE devices SET evicted_until='9999-12-31'::timestamptz WHERE user_id=$1 AND xray_uuid=$2`, userID, target)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `DELETE FROM uuid_eviction_acknowledgments WHERE epoch=(SELECT epoch FROM uuid_evictions WHERE device_uuid=$1)`, target)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO uuid_evictions(device_uuid,epoch,required_node_ids,status,requested_at,confirmed_at)
 VALUES($1,$2,$3::uuid[],'pending',NOW(),NULL)
 ON CONFLICT (device_uuid) DO UPDATE SET epoch=EXCLUDED.epoch,required_node_ids=EXCLUDED.required_node_ids,status='pending',requested_at=NOW(),confirmed_at=NULL`, target, epoch, expected)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return epoch, nil
}

// completeUUIDEvictions never fabricates success on timeout or DB/agent error.
// A pending row retains the ban until every required node acknowledges.
func completeUUIDEvictions(ctx context.Context, db *pgxpool.Pool, userID string) (bool, error) {
	// Confirmed rows remain banned until their cooldown has elapsed. Pending
	// rows have no expiry: absence of one Exit acknowledgment is not success.
	_, err := db.Exec(ctx, `UPDATE devices d SET evicted_until=e.confirmed_at+$2::interval
	 FROM uuid_evictions e WHERE e.device_uuid=d.xray_uuid AND d.user_id=$1
	 AND e.status='confirmed' AND d.evicted_until='9999-12-31'::timestamptz`, userID, evictionCooldown.String())
	if err != nil {
		return false, err
	}
	_, err = db.Exec(ctx, `DELETE FROM uuid_evictions e USING devices d
	 WHERE e.device_uuid=d.xray_uuid AND d.user_id=$1 AND e.status='confirmed'
	 AND d.evicted_until<=NOW()`, userID)
	if err != nil {
		return false, err
	}
	var pending bool
	err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM uuid_evictions e JOIN devices d ON d.xray_uuid=e.device_uuid WHERE d.user_id=$1 AND e.status='pending')`, userID).Scan(&pending)
	return pending, err
}

func eligibleEvictionExits(ctx context.Context, db *pgxpool.Pool, obs services.OnlineObservation) ([]string, error) {
	if !obs.Complete || len(obs.UnknownExitIDs) > 0 || obs.Ambiguous || obs.NewestUUID == "" {
		return nil, ErrIncompleteEvictionFleet
	}
	rows, err := db.Query(ctx, `SELECT id::text, COALESCE(shaping_mode,''), COALESCE(shaping_ok,false),
 COALESCE(xray_running,false),COALESCE(config_hash,''),last_seen>NOW()-INTERVAL '20 seconds'
 FROM nodes WHERE role IN ('exit','both') AND status<>'pending' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id, mode, hash string
		var shaping, running, fresh bool
		if err := rows.Scan(&id, &mode, &shaping, &running, &hash, &fresh); err != nil {
			return nil, err
		}
		if !fresh || !running || !shaping || mode != "device_global_v1" || hash == "" {
			return nil, ErrIncompleteEvictionFleet
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrIncompleteEvictionFleet
	}
	return ids, nil
}
