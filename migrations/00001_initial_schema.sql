-- +goose Up

-- admins
CREATE TABLE admins (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    totp_secret TEXT,
    totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- node_groups
CREATE TABLE node_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- plans
CREATE TABLE plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    traffic_limit BIGINT,
    duration_days INT NOT NULL,
    max_devices INT NOT NULL DEFAULT 1,
    speed_limit INT,
    node_group_id UUID NOT NULL REFERENCES node_groups(id),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- nodes
CREATE TABLE nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    country TEXT NOT NULL,
    region TEXT NOT NULL,
    ip INET NOT NULL,
    port INT NOT NULL DEFAULT 443,
    api_key TEXT NOT NULL UNIQUE,
    reg_token TEXT,
    status TEXT NOT NULL DEFAULT 'offline',
    last_seen TIMESTAMPTZ,
    xray_version TEXT,
    cpu_usage FLOAT,
    memory_usage FLOAT,
    disk_usage FLOAT,
    load_avg FLOAT,
    reality_private_key TEXT,
    reality_public_key TEXT,
    reality_short_id TEXT,
    tls_cert_path TEXT,
    tls_key_path TEXT,
    tls_cert_expiry TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- node_group_nodes (junction table)
CREATE TABLE node_group_nodes (
    node_group_id UUID NOT NULL REFERENCES node_groups(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY (node_group_id, node_id)
);

-- users
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    sub_token TEXT NOT NULL UNIQUE,
    plan_id UUID REFERENCES plans(id),
    plan_started_at TIMESTAMPTZ,
    plan_expires_at TIMESTAMPTZ,
    traffic_used BIGINT NOT NULL DEFAULT 0,
    traffic_reset_day INT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- devices
CREATE TABLE devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT,
    xray_uuid UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- plan_requests
CREATE TABLE plan_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES plans(id),
    status TEXT NOT NULL DEFAULT 'pending',
    reviewed_by UUID REFERENCES admins(id),
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- traffic_logs
CREATE TABLE traffic_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id),
    node_id UUID NOT NULL REFERENCES nodes(id),
    up_bytes BIGINT NOT NULL DEFAULT 0,
    dn_bytes BIGINT NOT NULL DEFAULT 0,
    logged_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- announcements
CREATE TABLE announcements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- settings
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_traffic_logs_device_logged_at ON traffic_logs(device_id, logged_at);
CREATE INDEX idx_traffic_logs_node_logged_at ON traffic_logs(node_id, logged_at);
CREATE INDEX idx_users_status ON users(status);
CREATE INDEX idx_users_plan_id ON users(plan_id);
CREATE INDEX idx_devices_user_id ON devices(user_id);
CREATE INDEX idx_plan_requests_user_status ON plan_requests(user_id, status);

-- +goose Down
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS announcements;
DROP TABLE IF EXISTS traffic_logs;
DROP TABLE IF EXISTS plan_requests;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS node_group_nodes;
DROP TABLE IF EXISTS nodes;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS node_groups;
DROP TABLE IF EXISTS admins;
