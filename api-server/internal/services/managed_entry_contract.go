package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrManagedEntryUnavailable = errors.New("managed Entry hostname unavailable")

type managedEntryQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ManagedEntryHostname reads only the stored hostname of the live node association.
func ManagedEntryHostname(ctx context.Context, db managedEntryQuerier, nodeID string) (string, error) {
	var hostname *string
	err := db.QueryRow(ctx, `SELECT hostname FROM managed_entry_dns WHERE node_id=$1`, nodeID).Scan(&hostname)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &RealitySNIError{Kind: RealitySNIEntryUnavailable, Cause: ErrManagedEntryUnavailable}
	}
	if err != nil {
		return "", fmt.Errorf("read managed Entry hostname: %w", err)
	}
	if hostname == nil || *hostname == "" {
		return "", &RealitySNIError{Kind: RealitySNIEntryUnavailable, Cause: ErrManagedEntryUnavailable}
	}
	return *hostname, nil
}
