package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UUID eviction contract: the scheduler inserts/replaces a row for each device
// UUID with a new epoch, status='pending', and a snapshot of every required Exit
// node in required_node_ids. It must keep the UUID banned until status='confirmed'.
// Replacing an epoch must delete its old acknowledgments in the same transaction.
// Empty required_node_ids is not a valid proof of fleetwide egress closure.
const uuidEvictionSchema = `
CREATE TABLE IF NOT EXISTS uuid_evictions (
 device_uuid TEXT PRIMARY KEY,
 epoch UUID NOT NULL UNIQUE,
 required_node_ids UUID[] NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed')),
 requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 confirmed_at TIMESTAMPTZ,
 CHECK (cardinality(required_node_ids) > 0)
);
CREATE TABLE IF NOT EXISTS uuid_eviction_acknowledgments (
 epoch UUID NOT NULL REFERENCES uuid_evictions(epoch) ON DELETE CASCADE,
 node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (epoch,node_id)
);`

var ErrEvictionEpoch = errors.New("eviction epoch is not current")
var ErrEvictionNode = errors.New("node is not required for eviction")

// AcknowledgeUUIDEviction accepts only the authenticated node's ID (never a
// client-supplied ID). The lock serializes epoch replacement and simultaneous
// acknowledgments; confirmation occurs only once every required node has acked.
func AcknowledgeUUIDEviction(ctx context.Context, db *pgxpool.Pool, deviceUUID, epoch, nodeID string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentEpoch, status string
	var required bool
	err = tx.QueryRow(ctx, `SELECT epoch::text,status,$2::uuid = ANY(required_node_ids)
 FROM uuid_evictions WHERE device_uuid=$1 FOR UPDATE`, deviceUUID, nodeID).Scan(&currentEpoch, &status, &required)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEvictionEpoch
	}
	if err != nil {
		return fmt.Errorf("load eviction: %w", err)
	}
	if currentEpoch != epoch {
		return ErrEvictionEpoch
	}
	if !required {
		return ErrEvictionNode
	}
	_, err = tx.Exec(ctx, `INSERT INTO uuid_eviction_acknowledgments(epoch,node_id) VALUES($1,$2)
 ON CONFLICT DO NOTHING`, epoch, nodeID)
	if err != nil {
		return fmt.Errorf("record eviction acknowledgment: %w", err)
	}
	if status == "pending" {
		_, err = tx.Exec(ctx, `UPDATE uuid_evictions e SET status='confirmed',confirmed_at=NOW()
   WHERE device_uuid=$1 AND epoch=$2 AND status='pending'
   AND NOT EXISTS (SELECT 1 FROM unnest(e.required_node_ids) AS required(node_id)
    WHERE NOT EXISTS (SELECT 1 FROM uuid_eviction_acknowledgments a
     WHERE a.epoch=e.epoch AND a.node_id=required.node_id))`, deviceUUID, epoch)
		if err != nil {
			return fmt.Errorf("confirm eviction: %w", err)
		}
	}
	return tx.Commit(ctx)
}
