package database

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
    tls_cert_file       TEXT,
    tls_key_file        TEXT,
    ss_password         TEXT,
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
	}
	for _, m := range migrations {
		if _, err := pool.Exec(ctx, m); err != nil {
			return fmt.Errorf("running migration %q: %w", m, err)
		}
	}

	return nil
}
