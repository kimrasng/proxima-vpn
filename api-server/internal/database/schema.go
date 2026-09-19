package database

// This file is the single source of truth for the database schema. It is applied
// idempotently at API startup by Migrate() (called from cmd/main.go). New tables
// belong in the schema constant below; incremental column additions for existing
// deployments go in the migrations slice inside Migrate().

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS admins (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email          TEXT NOT NULL UNIQUE,
    password_hash  TEXT NOT NULL,
    totp_secret    TEXT NOT NULL DEFAULT '',
    totp_enabled   BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS node_groups (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS plans (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    traffic_limit  BIGINT,
    duration_days  INT NOT NULL,
    max_devices    INT NOT NULL DEFAULT 1,
    speed_limit    INT,
    node_group_id  UUID NOT NULL REFERENCES node_groups(id),
    is_active      BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email            TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL DEFAULT '',
    password_hash    TEXT NOT NULL,
    sub_token        TEXT NOT NULL UNIQUE,
    plan_id          UUID REFERENCES plans(id),
    plan_started_at  TIMESTAMPTZ,
    plan_expires_at  TIMESTAMPTZ,
    traffic_used     BIGINT NOT NULL DEFAULT 0,
    traffic_reset_at TIMESTAMPTZ,
    telegram_id      BIGINT UNIQUE,
    is_active        BOOLEAN NOT NULL DEFAULT true,
    status           TEXT NOT NULL DEFAULT 'pending',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS nodes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL,
    reg_token           TEXT,
    api_key             TEXT NOT NULL,
    country             TEXT NOT NULL DEFAULT '',
    region              TEXT NOT NULL DEFAULT '',
    ip                  INET NOT NULL,
    port                INT NOT NULL DEFAULT 443,
    status              TEXT NOT NULL DEFAULT 'pending',
    xray_version        TEXT NOT NULL DEFAULT '',
    reality_private_key TEXT NOT NULL DEFAULT '',
    reality_public_key  TEXT NOT NULL DEFAULT '',
    reality_short_id    TEXT NOT NULL DEFAULT '',
    tls_domain          TEXT NOT NULL DEFAULT '',
    tls_email           TEXT NOT NULL DEFAULT '',
    tls_cert_file       TEXT,
    tls_key_file        TEXT,
    ss_password         TEXT,
    xray_target_version TEXT NOT NULL DEFAULT '',
    cpu_usage           REAL NOT NULL DEFAULT 0,
    memory_usage        REAL NOT NULL DEFAULT 0,
    disk_usage          REAL NOT NULL DEFAULT 0,
    load_avg            REAL NOT NULL DEFAULT 0,
    network_in          REAL NOT NULL DEFAULT 0,
    network_out         REAL NOT NULL DEFAULT 0,
    last_seen           TIMESTAMPTZ,
    last_ping_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS node_group_nodes (
    node_group_id UUID NOT NULL REFERENCES node_groups(id) ON DELETE CASCADE,
    node_id       UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY (node_group_id, node_id)
);

CREATE TABLE IF NOT EXISTS devices (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL DEFAULT 'Device',
    xray_uuid  TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS traffic_logs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id  UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    node_id    UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    up_bytes   BIGINT NOT NULL DEFAULT 0,
    dn_bytes   BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS plan_requests (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id     UUID NOT NULL REFERENCES plans(id),
    status      TEXT NOT NULL DEFAULT 'pending',
    reviewed_by UUID REFERENCES admins(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS announcements (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title      TEXT NOT NULL,
    content    TEXT NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_templates (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL UNIQUE,
    traffic_limit  BIGINT,
    duration_days  INT NOT NULL DEFAULT 30,
    max_devices    INT NOT NULL DEFAULT 1,
    speed_limit    INT,
    node_group_id  UUID REFERENCES node_groups(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS inbounds (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id    UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    protocol   TEXT NOT NULL,
    port       INT NOT NULL,
    tag        TEXT NOT NULL,
    settings   JSONB NOT NULL DEFAULT '{}',
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (node_id, port)
);
CREATE INDEX IF NOT EXISTS idx_inbounds_node_id ON inbounds(node_id);

INSERT INTO settings (key, value) VALUES ('subscription_update_interval', '3600')
ON CONFLICT (key) DO NOTHING;
`

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("running schema migration: %w", err)
	}

	migrations := []string{
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS network_in REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS network_out REAL NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS node_metrics_history (
			id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			node_id      UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			cpu_usage    REAL NOT NULL,
			memory_usage REAL NOT NULL,
			disk_usage   REAL NOT NULL,
			load_avg     REAL NOT NULL,
			network_in   REAL NOT NULL,
			network_out  REAL NOT NULL,
			recorded_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_node_metrics_history_node_recorded
			ON node_metrics_history(node_id, recorded_at DESC)`,
		`ALTER TABLE announcements ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`ALTER TABLE announcements ADD COLUMN IF NOT EXISTS image_url TEXT`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS xray_target_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS telegram_id BIGINT UNIQUE`,
		// Per-device WireGuard identity. wg_private_key/wg_public_key are the
		// device's own X25519 keypair (see pkg/crypto.GenerateWireGuardKeypair);
		// wg_address is its tunnel IP, allocated sequentially from wg_ip_seq
		// out of a fixed 10.66.0.0/16 pool (see handlers/user_device.go). All
		// three are NULL for devices that predate WireGuard support or were
		// never assigned a tunnel address.
		`CREATE SEQUENCE IF NOT EXISTS wg_ip_seq START 1`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS wg_private_key TEXT`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS wg_public_key TEXT`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS wg_address TEXT`,
		// tls_email is the ACME account email for a node's Let's Encrypt
		// certificate (see IssueCertificate in handlers/admin_node.go and the
		// node-agent tls poll loop in node-agent/cmd/main.go). Previously only
		// tls_domain was persisted, which nothing ever read back - the
		// node-agent had no way to learn a domain was requested, so
		// tls_cert_file/tls_key_file never got populated and vmess_ws/
		// trojan_tls inbounds silently never made it into the running Xray
		// config (see buildVmessWS/buildTrojanTLS in services/xray_config.go).
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS tls_email TEXT NOT NULL DEFAULT ''`,
		// Self-reported runtime state from the heartbeat. status alone only
		// tracked agent reachability, so a crashed Xray or a node on a stale
		// config looked healthy. config_hash is the sha256 of the config the
		// node actually applied (compare with GenerateDigest).
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS config_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS xray_running BOOLEAN NOT NULL DEFAULT false`,
		// Keeps the retention sweeps (scheduler/retention.go) off full scans.
		`CREATE INDEX IF NOT EXISTS idx_traffic_logs_created_at ON traffic_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_node_metrics_history_recorded_at ON node_metrics_history(recorded_at)`,
		// One protocol per node: Xray takes a single config per node, and mixing
		// protocols made speed limits bypassable via an uncapped inbound. Keyed
		// on node_id alone - (node_id, protocol) would only block duplicates of
		// the same protocol, the opposite of the rule. Guarded so a pre-existing
		// multi-inbound node cannot fail the migration and block startup.
		`DO $$
		 BEGIN
		   IF EXISTS (
		     SELECT 1 FROM inbounds GROUP BY node_id HAVING COUNT(*) > 1
		   ) THEN
		     RAISE NOTICE 'inbounds: skipping one-inbound-per-node index; existing nodes have several';
		   ELSE
		     CREATE UNIQUE INDEX IF NOT EXISTS idx_inbounds_one_protocol_per_node
		       ON inbounds(node_id);
		   END IF;
		 END $$`,

		// max_devices caps how many credentials a user may create; it never
		// capped how many of them connect at once, so a plan sold as "5 devices"
		// allowed unlimited concurrency. NULL means "fall back to max_devices"
		// so existing plans keep their advertised number without a data fix.
		`ALTER TABLE plans ADD COLUMN IF NOT EXISTS max_concurrent INT`,
		`ALTER TABLE user_templates ADD COLUMN IF NOT EXISTS max_concurrent INT`,

		// Set when a device is over its plan's concurrency cap. A deadline
		// rather than a boolean: evicting always makes the count look healthy
		// again, so re-admission has to be driven by time or the device flaps
		// in and out of the config every poll.
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS evicted_until TIMESTAMPTZ`,

		// Speed limits are enforced by tc on the node, which needs root and a
		// resolvable default route. When that fails the tunnel still works and
		// limited users simply run uncapped, so the node has to say so or the
		// silence reads as success.
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS shaping_ok BOOLEAN NOT NULL DEFAULT true`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS shaping_tiers INT NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS shaping_error TEXT NOT NULL DEFAULT ''`,

		// actor/target are untyped text rather than FKs: the feed has to outlive
		// the rows it describes, and a cascade delete would erase the record of
		// the deletion itself. target_* is null for events about the actor
		// alone, such as a login.
		`CREATE TABLE IF NOT EXISTS activity_logs (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			event_type  TEXT NOT NULL,
			severity    TEXT NOT NULL DEFAULT 'info',
			actor_type  TEXT NOT NULL DEFAULT 'system',
			actor_id    TEXT NOT NULL DEFAULT '',
			actor_label TEXT NOT NULL DEFAULT '',
			target_type TEXT,
			target_id   TEXT,
			detail      JSONB NOT NULL DEFAULT '{}',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_created_at ON activity_logs(created_at DESC)`,

		// What the dashboard's period-over-period deltas are measured against.
		// The live figures cannot supply one: online users and online nodes
		// exist only in Redis under a 60s TTL, so nothing can be asked what
		// they were yesterday. Swept by scheduler/retention.go.
		`CREATE TABLE IF NOT EXISTS dashboard_snapshots (
			id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			active_alerts       INT NOT NULL DEFAULT 0,
			online_nodes        INT NOT NULL DEFAULT 0,
			total_nodes         INT NOT NULL DEFAULT 0,
			online_users        INT NOT NULL DEFAULT 0,
			total_users         INT NOT NULL DEFAULT 0,
			traffic_today       BIGINT NOT NULL DEFAULT 0,
			recorded_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dashboard_snapshots_recorded_at
			ON dashboard_snapshots(recorded_at DESC)`,

		// Keeps the per-node traffic aggregation off a full scan of the window.
		`CREATE INDEX IF NOT EXISTS idx_traffic_logs_node_created
			ON traffic_logs(node_id, created_at DESC)`,
	}
	for _, m := range migrations {
		if _, err := pool.Exec(ctx, m); err != nil {
			return fmt.Errorf("running migration %q: %w", m, err)
		}
	}

	return nil
}
