package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type ConnectionHistoryRetention struct{ db *pgxpool.Pool }

func NewConnectionHistoryRetention(db *pgxpool.Pool) *ConnectionHistoryRetention {
	return &ConnectionHistoryRetention{db: db}
}

// RunOnce joins the existing retention scheduler's cadence. It touches only
// history tables. Receipt lifetime exceeds history + maximum replay + one day.
func (r *ConnectionHistoryRetention) RunOnce(ctx context.Context, now time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(current_database()),hashtext('connection-history-retention'))`); err != nil {
		return err
	}
	cutoff := now.UTC().Add(-services.ConnectionHistoryRetention)
	for _, table := range []string{"connection_flows", "connection_sessions"} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE last_received_at<$1`, cutoff); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM connection_log_gaps WHERE received_at<$1`, cutoff); err != nil {
		return err
	}
	// Partitions are named only by EnsureConnectionHistoryPartition; validate
	// that convention before formatting any identifier.
	rows, err := tx.Query(ctx, `SELECT child.relname FROM pg_inherits i JOIN pg_class parent ON parent.oid=i.inhparent JOIN pg_class child ON child.oid=i.inhrelid WHERE parent.oid='connection_log_events'::regclass`)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		const prefix = "connection_log_events_"
		if len(name) != len(prefix)+8 || name[:len(prefix)] != prefix {
			rows.Close()
			return fmt.Errorf("unexpected history partition %q", name)
		}
		day, e := time.Parse("20060102", name[len(prefix):])
		if e != nil {
			rows.Close()
			return e
		}
		if !day.Add(24 * time.Hour).After(cutoff) {
			drop = append(drop, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range drop {
		if _, err = tx.Exec(ctx, `DROP TABLE `+pgx.Identifier{name}.Sanitize()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM connection_log_events WHERE received_at<$1`, cutoff); err != nil {
		return err
	}
	for _, table := range []string{"connection_log_batches", "connection_log_event_receipts"} {
		if _, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE received_at<$1`, now.Add(-services.ConnectionHistoryReceiptRetention)); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM connection_log_bindings b WHERE revoked_at<$1
 AND NOT EXISTS(SELECT 1 FROM connection_flows p WHERE p.node_origin=b.node_origin AND p.payload->'auth'->>'credential_ref'=b.credential_ref::text)
 AND NOT EXISTS(SELECT 1 FROM connection_sessions p WHERE p.node_origin=b.node_origin AND p.payload->'auth'->>'credential_ref'=b.credential_ref::text)`, now.Add(-services.ConnectionHistoryReceiptRetention))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
