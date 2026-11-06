package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectionHistorySchema is executed by the integration owner inside Migrate's
// existing advisory lock. Origin IDs and snapshots deliberately outlive live FKs.
const ConnectionHistorySchema = `
CREATE TABLE IF NOT EXISTS connection_log_controls (
 node_origin UUID PRIMARY KEY,
 node_id UUID REFERENCES nodes(id) ON DELETE SET NULL,
 node_snapshot TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT false,
 revision NUMERIC(20,0) NOT NULL DEFAULT 0,
 collection_epoch UUID,
 applied_revision NUMERIC(20,0) NOT NULL DEFAULT 0,
 applied_enabled BOOLEAN NOT NULL DEFAULT false,
 state TEXT NOT NULL DEFAULT 'off',
 applied_at TIMESTAMPTZ,
 last_config_fetch_at TIMESTAMPTZ,
 status_at TIMESTAMPTZ,
 status JSONB NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS connection_log_epochs (
 node_origin UUID NOT NULL,
 epoch UUID NOT NULL,
 issued_at TIMESTAMPTZ NOT NULL,
 closed_at TIMESTAMPTZ,
 barriers JSONB NOT NULL DEFAULT '{}',
 PRIMARY KEY(node_origin,epoch)
);
CREATE TABLE IF NOT EXISTS connection_log_revisions (
 node_origin UUID NOT NULL,
 revision NUMERIC(20,0) NOT NULL,
 epoch UUID,
 enabled BOOLEAN NOT NULL,
 issued_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(node_origin,revision)
);
CREATE TABLE IF NOT EXISTS connection_log_bindings (
 node_origin UUID NOT NULL,
 credential_ref UUID NOT NULL,
 binding_revision NUMERIC(20,0) NOT NULL,
 manifest JSONB NOT NULL,
 generation TEXT,
 user_id UUID REFERENCES users(id) ON DELETE SET NULL,
 device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
 user_snapshot TEXT,
 device_snapshot TEXT,
 issued_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ,
 PRIMARY KEY(node_origin,credential_ref),
 UNIQUE(node_origin,credential_ref,binding_revision)
);
ALTER TABLE connection_log_bindings ADD COLUMN IF NOT EXISTS generation TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS connection_log_bindings_active_label ON connection_log_bindings
 (node_origin,(manifest->>'runtime'),(manifest->>'inbound_tag'),(manifest->>'label_kind'),(manifest->>'label'))
 WHERE revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS connection_log_producers (
 node_origin UUID NOT NULL,
 epoch UUID NOT NULL,
 producer_id TEXT NOT NULL,
 boot_id UUID NOT NULL,
 authenticated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(node_origin,epoch,producer_id,boot_id)
);
CREATE TABLE IF NOT EXISTS connection_log_batches (
 node_origin UUID NOT NULL,
 batch_id UUID NOT NULL,
 payload_sha256 TEXT NOT NULL,
 status TEXT NOT NULL,
 received_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(node_origin,batch_id)
);
CREATE TABLE IF NOT EXISTS connection_log_event_receipts (
 node_origin UUID NOT NULL,
 event_id TEXT NOT NULL,
 payload_sha256 TEXT NOT NULL,
 received_at TIMESTAMPTZ NOT NULL,
 expired BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(node_origin,event_id)
);
ALTER TABLE connection_log_event_receipts ADD COLUMN IF NOT EXISTS expired BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS connection_log_events (
 node_origin UUID NOT NULL,
 event_id TEXT NOT NULL,
 received_at TIMESTAMPTZ NOT NULL,
 payload JSONB NOT NULL
) PARTITION BY RANGE(received_at);
CREATE INDEX IF NOT EXISTS connection_log_events_entity_sequence ON connection_log_events
 (node_origin,(payload->>'collection_epoch'),(coalesce(payload->>'flow_id',payload->>'tunnel_id')),((payload->>'sequence')::numeric));
CREATE TABLE IF NOT EXISTS connection_sessions (
 node_origin UUID NOT NULL,
 epoch UUID NOT NULL,
 entity_id TEXT NOT NULL,
 producer_id TEXT NOT NULL,
 boot_id UUID NOT NULL,
 sequence NUMERIC(20,0) NOT NULL,
 first_observed_at TIMESTAMPTZ NOT NULL,
 last_received_at TIMESTAMPTZ NOT NULL,
 started_at TIMESTAMPTZ,
 ended_at TIMESTAMPTZ,
 observation_status TEXT NOT NULL,
 payload JSONB NOT NULL,
 PRIMARY KEY(node_origin,epoch,entity_id)
);
CREATE TABLE IF NOT EXISTS connection_flows (
 node_origin UUID NOT NULL,
 epoch UUID NOT NULL,
 entity_id TEXT NOT NULL,
 producer_id TEXT NOT NULL,
 boot_id UUID NOT NULL,
 sequence NUMERIC(20,0) NOT NULL,
 first_observed_at TIMESTAMPTZ NOT NULL,
 last_received_at TIMESTAMPTZ NOT NULL,
 started_at TIMESTAMPTZ,
 ended_at TIMESTAMPTZ,
 observation_status TEXT NOT NULL,
 layer TEXT NOT NULL,
 upload NUMERIC(20,0),
 download NUMERIC(20,0),
 payload JSONB NOT NULL,
 PRIMARY KEY(node_origin,epoch,entity_id)
);
CREATE INDEX IF NOT EXISTS connection_flows_cursor ON connection_flows(first_observed_at DESC,node_origin DESC,epoch DESC,entity_id DESC);
CREATE INDEX IF NOT EXISTS connection_sessions_cursor ON connection_sessions(first_observed_at DESC,node_origin DESC,epoch DESC,entity_id DESC);
CREATE TABLE IF NOT EXISTS connection_log_gaps (
 node_origin UUID NOT NULL,
 event_id TEXT NOT NULL,
 epoch UUID NOT NULL,
 received_at TIMESTAMPTZ NOT NULL,
 payload JSONB NOT NULL,
 PRIMARY KEY(node_origin,event_id)
);
`

// MigrateConnectionHistory supports isolated tests/startup integration without a
// second migration engine. Do not call this while already holding Migrate's lock;
// execute ConnectionHistorySchema on that connection instead.
func MigrateConnectionHistory(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin history migration: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(current_database()),hashtext('proxima-vpn-database-migrate'))`); err != nil {
		return fmt.Errorf("lock history migration: %w", err)
	}
	if _, err = tx.Exec(ctx, ConnectionHistorySchema); err != nil {
		return fmt.Errorf("migrate history: %w", err)
	}
	return tx.Commit(ctx)
}

// EnsureConnectionHistoryPartition creates one UTC receipt-day partition.
// Event identity lives in the unpartitioned receipt ledger, not a per-day index.
func EnsureConnectionHistoryPartition(ctx context.Context, tx pgx.Tx, now time.Time) error {
	day := now.UTC().Truncate(24 * time.Hour)
	name := "connection_log_events_" + day.Format("20060102")
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('connection-history-partition'),hashtext($1))`, name); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF connection_log_events FOR VALUES FROM ('%s') TO ('%s')`,
		pgx.Identifier{name}.Sanitize(), day.Format(time.RFC3339), day.Add(24*time.Hour).Format(time.RFC3339)))
	return err
}
