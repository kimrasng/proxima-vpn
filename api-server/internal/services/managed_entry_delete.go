package services

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LockManagedEntryDNSOwner serializes deletion with future worker sessions by immutable owner ID.
// Call before locking the node or managed_entry_dns row in a transaction.
func LockManagedEntryDNSOwner(ctx context.Context, tx pgx.Tx, ownerNodeID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text, 29029))`, ownerNodeID)
	if err != nil {
		return fmt.Errorf("lock entry DNS owner: %w", err)
	}
	return nil
}

// RequestManagedEntryDNSDeletion marks only a live row; repeated requests do not advance generation.
func RequestManagedEntryDNSDeletion(ctx context.Context, tx pgx.Tx, locked *LockedRealityNode) error {
	if err := locked.check(tx); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE managed_entry_dns SET desired_action='delete', dns_status='deleting',
		generation=generation+1, cleanup_requested_at=COALESCE(cleanup_requested_at,NOW()),
		next_attempt_at=NOW(), updated_at=NOW()
		WHERE owner_node_id=$1 AND desired_action <> 'delete'`, locked.id)
	if err != nil {
		return fmt.Errorf("request entry DNS deletion: %w", err)
	}
	return nil
}

func DeleteNodeWithManagedDNS(ctx context.Context, pool *pgxpool.Pool, nodeID string) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin node deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := LockManagedEntryDNSOwner(ctx, tx, nodeID); err != nil {
		return "", err
	}
	locked, err := LockRealityNode(ctx, tx, nodeID)
	if err != nil {
		return "", err
	}
	if err := RequestManagedEntryDNSDeletion(ctx, tx, locked); err != nil {
		return "", err
	}
	var name string
	if err := tx.QueryRow(ctx, `DELETE FROM nodes WHERE id=$1 RETURNING name`, nodeID).Scan(&name); err != nil {
		return "", fmt.Errorf("delete node: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit node deletion: %w", err)
	}
	return name, nil
}
