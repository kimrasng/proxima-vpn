package database

// This file is the single source of truth for the database schema. It is applied
// idempotently at API startup by Migrate() (called from cmd/main.go). New tables
// belong in the schema constant below; incremental column additions for existing
// deployments go in the migrations slice inside Migrate().

import (
	"context"
	"errors"
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
    advertise      BOOLEAN NOT NULL DEFAULT false,
    is_advertised  BOOLEAN NOT NULL DEFAULT false,
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

-- Never prune this ledger without an enforced maximum agent replay age: an old
-- batch replayed after pruning would otherwise be billed a second time.
CREATE TABLE IF NOT EXISTS traffic_batches (
    node_id      UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    batch_id     UUID NOT NULL,
    payload_hash TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (node_id, batch_id)
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

-- Subscription domains are a shared pool, not assignments to individual users:
-- the host-agnostic /sub route can serve the same token through every domain.
CREATE TABLE IF NOT EXISTS subscription_domains (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain                TEXT NOT NULL UNIQUE,
    is_enabled            BOOLEAN NOT NULL DEFAULT true,
    is_public             BOOLEAN NOT NULL DEFAULT true,
    sort_order            INT NOT NULL DEFAULT 0,
    is_default            BOOLEAN NOT NULL DEFAULT false,
    dns_resolved          BOOLEAN NOT NULL DEFAULT false,
    dns_addresses         TEXT[] NOT NULL DEFAULT '{}',
    dns_error             TEXT NOT NULL DEFAULT '',
    tls_reachable         BOOLEAN NOT NULL DEFAULT false,
    tls_error             TEXT NOT NULL DEFAULT '',
    cert_expires_at       TIMESTAMPTZ,
    last_health_check_at  TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (BTRIM(domain) <> '')
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_domains_one_default
    ON subscription_domains(is_default) WHERE is_default;
CREATE INDEX IF NOT EXISTS idx_subscription_domains_public_order
    ON subscription_domains(is_enabled, is_public, sort_order, created_at)
    WHERE is_enabled AND is_public;

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

func Migrate(ctx context.Context, pool *pgxpool.Pool) (migrateErr error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring migration connection: %w", err)
	}
	defer conn.Release()

	const lockKey = "proxima-vpn-database-migrate"
	if _, err := conn.Exec(ctx,
		`SELECT pg_advisory_lock(hashtext(current_database()), hashtext($1))`, lockKey,
	); err != nil {
		return fmt.Errorf("acquiring migration advisory lock: %w", err)
	}
	defer func() {
		var unlocked bool
		unlockCtx := context.WithoutCancel(ctx)
		if err := conn.QueryRow(unlockCtx,
			`SELECT pg_advisory_unlock(hashtext(current_database()), hashtext($1))`, lockKey,
		).Scan(&unlocked); err != nil {
			closeErr := conn.Conn().Close(unlockCtx)
			migrateErr = errors.Join(migrateErr, fmt.Errorf("releasing migration advisory lock: %w", err), closeErr)
			return
		}
		if !unlocked {
			migrateErr = errors.Join(migrateErr, errors.New("migration advisory lock was not held"))
		}
	}()

	_, err = conn.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("running schema migration: %w", err)
	}

	if _, err := conn.Exec(ctx, uuidEvictionSchema); err != nil {
		return fmt.Errorf("creating UUID eviction tables: %w", err)
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
		// Existing and new plans require explicit final publication approval.
		`ALTER TABLE plans ADD COLUMN IF NOT EXISTS advertise BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE plans ADD COLUMN IF NOT EXISTS is_advertised BOOLEAN NOT NULL DEFAULT false`,

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

		// When the node last flipped between online and offline. Distinct from
		// updated_at, which also moves for TLS and Xray config edits, and from
		// last_seen, which moves on every heartbeat: neither answers "how long
		// has it been in this state".
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ`,

		// Scarce nodes can bill traffic at a premium: the quota charged to the
		// user is the transferred bytes times this factor. Only the quota is
		// scaled, never traffic_logs, so the per-node charts keep reporting the
		// bytes that actually crossed the wire.
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS traffic_multiplier NUMERIC(5,2) NOT NULL DEFAULT 1.0`,
		// Guarded by a catalog lookup because ADD CONSTRAINT has no IF NOT
		// EXISTS, and a bare retry would abort startup on the second run.
		`DO $$
		 BEGIN
		   IF NOT EXISTS (
		     SELECT 1 FROM pg_constraint WHERE conname = 'nodes_traffic_multiplier_range'
		   ) THEN
		     ALTER TABLE nodes ADD CONSTRAINT nodes_traffic_multiplier_range
		       CHECK (traffic_multiplier > 0 AND traffic_multiplier <= 100);
		   END IF;
		 END $$`,

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

		// user_templates duplicated every plans column and was never read by
		// anything: users carry plan_id, and nothing ever carried a template id.
		`DROP TABLE IF EXISTS user_templates`,

		// One row per (node_id, kind), holding the current lifecycle state of one
		// alert condition. The row IS the dedup key, so "is this already firing"
		// is a unique-index lookup rather than a scan for the newest open
		// episode. Bounded at nodes x kinds, so it is deliberately absent from
		// the retention sweep - pruning it would delete live state.
		//
		// breach_since/clear_since carry the sustained-breach windows. Keeping
		// them here rather than deriving them from node_metrics_history makes
		// evaluation O(1) per row and survives a process restart.
		//
		// node_id deliberately carries NO foreign key. An alert is a record of
		// something that happened, not a property of the node it happened on, so
		// deleting a node must not erase the evidence that it was at 97% disk
		// when it was retired - the same reason activity_logs holds a bare
		// target_id. node_name/country/region are snapshots rather than joins for
		// the same reason: after the node row is gone there is nothing left to
		// join to. They are rewritten on every evaluation, so a rename is picked
		// up while the node exists and frozen once it does not.
		`CREATE TABLE IF NOT EXISTS node_alerts (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			node_id        UUID NOT NULL,
			node_name      TEXT NOT NULL DEFAULT '',
			node_country   TEXT NOT NULL DEFAULT '',
			node_region    TEXT NOT NULL DEFAULT '',
			kind           TEXT NOT NULL,
			state          TEXT NOT NULL DEFAULT 'ok',
			severity       TEXT NOT NULL DEFAULT 'warning',
			value          REAL NOT NULL DEFAULT 0,
			breach_since   TIMESTAMPTZ,
			clear_since    TIMESTAMPTZ,
			fired_at       TIMESTAMPTZ,
			resolved_at    TIMESTAMPTZ,
			notified_at    TIMESTAMPTZ,
			acked_at       TIMESTAMPTZ,
			acked_by       TEXT NOT NULL DEFAULT '',
			silenced_until TIMESTAMPTZ,
			closed_at      TIMESTAMPTZ,
			closed_by      TEXT NOT NULL DEFAULT '',
			evaluated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (node_id, kind)
		)`,
		// The read path only ever wants the conditions that are not ok, and a
		// partial index keeps that lookup off the resolved rows.
		`CREATE INDEX IF NOT EXISTS idx_node_alerts_open
			ON node_alerts(state) WHERE state <> 'ok'`,

		// Ranks severities so the alert upsert can tell an escalation from a
		// de-escalation and drop an acknowledgement that was made at the lower
		// tier. Must stay in step with severityRank in services/alert_rules.go.
		`CREATE OR REPLACE FUNCTION severity_rank(s TEXT) RETURNS INT AS $$
			SELECT CASE s WHEN 'error' THEN 2 WHEN 'warning' THEN 1 ELSE 0 END
		$$ LANGUAGE SQL IMMUTABLE`,

		// ActivityService.ListForTarget filters on target_type/target_id but only
		// created_at was indexed, so the per-node feed scanned the whole table.
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_target
			ON activity_logs(target_type, target_id, created_at DESC)`,

		// One row per login attempt, successful or not. activity_logs cannot
		// serve this: with no typed outcome and no failure reason, "this
		// account's failed attempts" means filtering JSONB across the global
		// feed.
		//
		// user_id is nullable and SET NULL on delete because an attempt against
		// an address that never existed - or an account since removed - is
		// exactly the attempt worth keeping; attempted_email preserves what was
		// typed either way.
		//
		// failure_reason is written only from the constants in
		// services/login_history.go, never from request text: this endpoint is
		// reachable unauthenticated, so the caller must not choose the content.
		`CREATE TABLE IF NOT EXISTS login_history (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
			actor_type      TEXT NOT NULL DEFAULT 'user',
			attempted_email TEXT NOT NULL DEFAULT '',
			success         BOOLEAN NOT NULL DEFAULT false,
			failure_reason  TEXT NOT NULL DEFAULT '',
			ip              TEXT NOT NULL DEFAULT '',
			user_agent      TEXT NOT NULL DEFAULT '',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// The admin panel only ever asks for one user's attempts, newest first.
		`CREATE INDEX IF NOT EXISTS idx_login_history_user_created
			ON login_history(user_id, created_at DESC)`,
		// Retention sweeps by age, and the global view is newest-first.
		`CREATE INDEX IF NOT EXISTS idx_login_history_created_at
			ON login_history(created_at DESC)`,

		// The per-user traffic breakdown reaches a user's rows through devices,
		// and neither hop was indexed for that direction: without these the
		// breakdown seq-scans every traffic row in the window for all users.
		`CREATE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_logs_device_created
			ON traffic_logs(device_id, created_at DESC)`,

		// Provisioning intent captured before the install command is issued. The
		// agent reports name/country/region again at registration, so these record
		// what the operator asked for and let registration keep it rather than
		// overwrite it with autodetection.
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS os_family TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS max_concurrent_conns INT NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS firewall_preset TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS firewall_ports TEXT NOT NULL DEFAULT ''`,
		// 0 means no cap rather than "refuse everything".
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'nodes_max_concurrent_conns_check'
			) THEN
				ALTER TABLE nodes ADD CONSTRAINT nodes_max_concurrent_conns_check
					CHECK (max_concurrent_conns >= 0);
			END IF;
		END $$`,

		// Alerts were originally a child of nodes: a foreign key with ON DELETE
		// CASCADE, plus a join for the node's name and location. That made
		// retiring a node silently destroy every alert it had ever raised, and
		// the join meant a surviving row would have been invisible anyway. An
		// alert outlives its node, so both links are cut here.
		//
		// The constraint is found by catalog lookup rather than by name: the
		// generated name is predictable but not guaranteed, and a database
		// restored through a tool that renamed it would silently keep cascading.
		`DO $$ DECLARE fk TEXT; BEGIN
			SELECT conname INTO fk FROM pg_constraint
			WHERE conrelid = 'node_alerts'::regclass AND contype = 'f'
			  AND confrelid = 'nodes'::regclass
			LIMIT 1;
			IF fk IS NOT NULL THEN
				EXECUTE format('ALTER TABLE node_alerts DROP CONSTRAINT %I', fk);
			END IF;
		END $$`,
		`ALTER TABLE node_alerts ADD COLUMN IF NOT EXISTS node_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE node_alerts ADD COLUMN IF NOT EXISTS node_country TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE node_alerts ADD COLUMN IF NOT EXISTS node_region TEXT NOT NULL DEFAULT ''`,
		// Operator-owned closure, for an alert the evaluator can never resolve
		// because the node it describes stopped reporting or no longer exists.
		`ALTER TABLE node_alerts ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ`,
		`ALTER TABLE node_alerts ADD COLUMN IF NOT EXISTS closed_by TEXT NOT NULL DEFAULT ''`,
		// Backfill the snapshots for rows written before they existed. Only rows
		// whose node still exists can be recovered; the rest keep the empty
		// string, which the read path renders as an unknown node.
		`UPDATE node_alerts a SET
			node_name = n.name, node_country = n.country, node_region = n.region
		 FROM nodes n WHERE n.id = a.node_id AND a.node_name = ''`,

		// Empty means the panel default, so existing users keep their output.
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT ''`,

		// Separate from nodes.name so that name survives as the fallback.
		`CREATE TABLE IF NOT EXISTS node_labels (
			node_id  UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			language TEXT NOT NULL,
			name     TEXT NOT NULL,
			PRIMARY KEY (node_id, language)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_node_labels_node ON node_labels(node_id)`,

		// Rows created before GrantPlan existed (or via the pre-fix admin
		// Create()) can carry plan_id with plan_expires_at left NULL. Both
		// the reset scheduler's and ExpiryCheckScheduler's WHERE clauses
		// require plan_expires_at IS NOT NULL, so such a row's plan never
		// resets or expires. Backfilling from plan_started_at (falling back
		// to created_at if that is also NULL) plus the plan's duration makes
		// them visible to both going forward; it does not change what they
		// see today, since nothing currently reads plan_expires_at as NULL
		// meaning "unlimited".
		`UPDATE users u SET plan_started_at = COALESCE(u.plan_started_at, u.created_at)
		 WHERE u.plan_id IS NOT NULL AND u.plan_started_at IS NULL`,
		`UPDATE users u SET plan_expires_at = u.plan_started_at + make_interval(days => p.duration_days)
		 FROM plans p
		 WHERE u.plan_id = p.id AND u.plan_expires_at IS NULL`,

		// A plan's legacy duration_days remains what an admin-triggered grant
		// (Telegram /setplan, direct admin assignment) gives - separate from
		// what is actually sellable at multiple durations here. The two may
		// legitimately disagree, so this is not a default flag on plan_prices.
		`CREATE TABLE IF NOT EXISTS plan_prices (
			plan_id       UUID   NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
			duration_days INT    NOT NULL CHECK (duration_days > 0),
			price_cents   BIGINT NOT NULL CHECK (price_cents >= 0),
			PRIMARY KEY (plan_id, duration_days)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_prices_plan ON plan_prices(plan_id)`,

		// duration_days and price_cents are snapshots of plan_prices at order
		// time, not references to it - an admin re-pricing a plan must not
		// change what an already-placed order is charged. No "expired" state:
		// with no payment gateway there is no deadline to expire against: an
		// admin cancel covers a stale order. The partial unique index caps a
		// user at one open order, which also rules out a double grant from
		// two pending orders both later marked paid.
		`CREATE TABLE IF NOT EXISTS plan_orders (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id       UUID   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			plan_id       UUID   NOT NULL REFERENCES plans(id),
			duration_days INT    NOT NULL CHECK (duration_days > 0),
			price_cents   BIGINT NOT NULL CHECK (price_cents >= 0),
			status        TEXT   NOT NULL DEFAULT 'pending'
			              CHECK (status IN ('pending','paid','cancelled')),
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			paid_at       TIMESTAMPTZ,
			paid_by       TEXT   NOT NULL DEFAULT '',
			cancelled_at  TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_orders_user ON plan_orders(user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_orders_status ON plan_orders(status, created_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_orders_one_pending
		 ON plan_orders(user_id) WHERE status = 'pending'`,

		// position and included are language-independent: keying them per
		// language would let ko mark a bullet included while en marks the
		// same position excluded. plan_feature_texts holds only the text,
		// FK'd to (plan_id, position) so a text row cannot outlive its bullet.
		`CREATE TABLE IF NOT EXISTS plan_features (
			plan_id  UUID    NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
			position INT     NOT NULL CHECK (position >= 0),
			included BOOLEAN NOT NULL DEFAULT true,
			PRIMARY KEY (plan_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS plan_feature_texts (
			plan_id  UUID NOT NULL,
			position INT  NOT NULL,
			language TEXT NOT NULL,
			text     TEXT NOT NULL,
			PRIMARY KEY (plan_id, position, language),
			FOREIGN KEY (plan_id, position)
				REFERENCES plan_features(plan_id, position) ON DELETE CASCADE
		)`,

		// One row per inbound confirmation attempt, written before any grant
		// runs. plan_orders.status answers "is this order settled"; this table
		// answers "have I already seen this exact provider event" - different
		// questions, since one order can receive several distinct events and
		// one event can be delivered many times (Stripe retries aggressively).
		//
		// (provider, external_id) is the idempotency key. For Stripe it is the
		// event id (evt_...), not the session id: retries preserve the event
		// id, and a captured/replayed payload carries it too, while one
		// session can emit several genuinely different events. The admin
		// provider has no external id, so callers derive one ("order:<id>"),
		// which encodes that an order may be admin-confirmed exactly once.
		//
		// order_id carries no foreign key: this row has to survive the order
		// it was about, the same reason activity_logs holds a bare target_id.
		// Nullable because a webhook resolving to no known order must still be
		// recorded before it is rejected.
		`CREATE TABLE IF NOT EXISTS payment_events (
			id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			provider     TEXT NOT NULL,
			external_id  TEXT NOT NULL,
			order_id     UUID,
			amount_cents BIGINT NOT NULL DEFAULT 0,
			currency     TEXT   NOT NULL DEFAULT '',
			session_id   TEXT   NOT NULL DEFAULT '',
			outcome      TEXT   NOT NULL DEFAULT 'received'
			             CHECK (outcome IN ('received','granted','duplicate','ignored','failed')),
			reason       TEXT   NOT NULL DEFAULT '',
			payload      JSONB  NOT NULL DEFAULT '{}',
			request_ip   TEXT   NOT NULL DEFAULT '',
			user_agent   TEXT   NOT NULL DEFAULT '',
			request_id   TEXT   NOT NULL DEFAULT '',
			actor_type   TEXT   NOT NULL DEFAULT '',
			actor_id     TEXT   NOT NULL DEFAULT '',
			received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			processed_at TIMESTAMPTZ,
			UNIQUE (provider, external_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_payment_events_order ON payment_events(order_id, received_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_payment_events_received ON payment_events(received_at DESC)`,

		// A hosted checkout needs a deadline: an abandoned Stripe session
		// otherwise leaves a pending order holding the user's one-pending-order
		// slot forever. Backfilled so existing rows are visible to the sweeper.
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS expired_at TIMESTAMPTZ`,
		// Which provider the user was last handed off to, and that provider's
		// own handle - snapshots for support/audit only. The webhook resolves
		// its order through the event payload's metadata, never through this.
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS provider_session_id TEXT NOT NULL DEFAULT ''`,
		`UPDATE plan_orders SET expires_at = created_at + INTERVAL '24 hours' WHERE expires_at IS NULL`,
		// 'expired' is a fourth state, distinct from 'cancelled': a cancel is a
		// refusal and must never be granted, while an expiry is only a timeout
		// and a late confirmation is still honoured (see PaymentService.Settle).
		`DO $$ DECLARE ck TEXT; BEGIN
			SELECT conname INTO ck FROM pg_constraint
			WHERE conrelid = 'plan_orders'::regclass AND contype = 'c'
			  AND pg_get_constraintdef(oid) LIKE '%pending%paid%cancelled%';
			IF ck IS NOT NULL THEN
				EXECUTE format('ALTER TABLE plan_orders DROP CONSTRAINT %I', ck);
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'plan_orders_status_check2') THEN
				ALTER TABLE plan_orders ADD CONSTRAINT plan_orders_status_check2
					CHECK (status IN ('pending','paid','cancelled','expired'));
			END IF;
		END $$`,
		`CREATE INDEX IF NOT EXISTS idx_plan_orders_pending_expiry ON plan_orders(expires_at) WHERE status = 'pending'`,
		// Superseded by the checkout order flow (plan_orders/payment_events):
		// requests were a manual work queue, checkout grants automatically.
		`DROP TABLE IF EXISTS plan_requests`,

		// A promotion code's discount, validity window, and every eligibility
		// rule it can carry. plan_ids/duration_days/allowed_user_ids are NULL
		// for "no restriction" rather than empty arrays, so a wide-open code
		// does not need every plan/duration/user enumerated. redeemed_count is
		// the atomic capacity counter PromotionService.Reserve increments
		// inside a single conditional UPDATE - see promotion_redemptions below
		// for the per-order, per-user ledger that counter is derived from.
		`CREATE TABLE IF NOT EXISTS promotion_codes (
			id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			code                   TEXT NOT NULL UNIQUE,
			discount_type          TEXT NOT NULL CHECK (discount_type IN ('percent','fixed')),
			discount_value         BIGINT NOT NULL CHECK (discount_value > 0),
			valid_from             TIMESTAMPTZ NOT NULL,
			valid_until            TIMESTAMPTZ NOT NULL,
			min_order_cents        BIGINT NOT NULL DEFAULT 0 CHECK (min_order_cents >= 0),
			max_redemptions        INT,
			max_redemptions_per_user INT NOT NULL DEFAULT 1 CHECK (max_redemptions_per_user > 0),
			first_purchase_only    BOOLEAN NOT NULL DEFAULT false,
			plan_ids               UUID[],
			duration_days          INT[],
			allowed_user_ids       UUID[],
			redeemed_count         INT NOT NULL DEFAULT 0,
			is_active              BOOLEAN NOT NULL DEFAULT true,
			created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CHECK (valid_until > valid_from),
			CHECK (discount_type <> 'percent' OR discount_value <= 100)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_promotion_codes_code_active ON promotion_codes(code) WHERE is_active`,

		// One row per order that reserved a code, which is what makes the
		// per-user cap and the "one code per order" rule countable rather than
		// inferred from plan_orders (an order can be cancelled and its
		// promotion_id cleared, but the redemption row must still count against
		// the user's cap for the window it held the reservation). status
		// distinguishes a reservation still backing a live order (reserved),
		// one that paid out (confirmed), and one released back to the pool by
		// a cancel or an expiry sweep (released) - see PromotionService.Release.
		`CREATE TABLE IF NOT EXISTS promotion_redemptions (
			id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			promotion_id UUID NOT NULL REFERENCES promotion_codes(id),
			user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			order_id     UUID NOT NULL UNIQUE REFERENCES plan_orders(id) ON DELETE CASCADE,
			status       TEXT NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved','confirmed','released')),
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_promotion_redemptions_promo_user ON promotion_redemptions(promotion_id, user_id) WHERE status <> 'released'`,

		// Snapshots on the order itself: promotion_id says which code (if any)
		// discounted this order, discount_cents is the amount taken off. Both
		// survive the code being edited or deactivated later, and price_cents
		// remains the final, post-discount total PaymentService.Settle
		// cross-checks against a provider's confirmed amount - the discount is
		// baked into the one number every existing settle/refund path already
		// trusts, rather than a second figure everything downstream would need
		// to know to subtract.
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS promotion_id UUID REFERENCES promotion_codes(id)`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS discount_cents BIGINT NOT NULL DEFAULT 0 CHECK (discount_cents >= 0)`,

		// PHS-026: Checkout audit and grant evidence for plan orders.
		// Tracking columns (origin, client_ip, user_agent, browser_family, os_family, locale, device_fingerprint)
		// are NULLable to distinguish never-captured from purged by retention sweeps. Grant timestamps
		// (granted_plan_expires_before/after) are retained through purge cycles as financial facts.
		// A partial purge-sweep index keeps retention off full scans when NULLing tracking fields.
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS origin TEXT`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS client_ip TEXT`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS user_agent TEXT`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS browser_family TEXT`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS os_family TEXT`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS locale TEXT`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS device_fingerprint TEXT`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS granted_plan_expires_before TIMESTAMPTZ`,
		`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS granted_plan_expires_after TIMESTAMPTZ`,
		// Partial index keyed to the retention sweep termination predicate: support fast purge scans by
		// identifying orders where user_agent is still present (non-NULL user_agent is the sweep exit condition).
		`CREATE INDEX IF NOT EXISTS idx_plan_orders_purge_sweep ON plan_orders(created_at) WHERE user_agent IS NOT NULL`,

		// A node's place in the data path. 'exit' terminates client tunnels and
		// reaches the internet (what every node did before chains existed, hence
		// the default); 'relay' only forwards opaque bytes to an exit; 'both'
		// does either. publish_direct=false hides a node's own address from
		// subscriptions, which is how a relay-only exit stops being handed out
		// directly. It hides the address, it does not close the port - the
		// node's firewall is what restricts the exit to its relays.
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'exit'`,
		`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS publish_direct BOOLEAN NOT NULL DEFAULT true`,
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'nodes_role_check'
			) THEN
				ALTER TABLE nodes ADD CONSTRAINT nodes_role_check
					CHECK (role IN ('exit','relay','both'));
			END IF;
		END $$`,

		// Each new relayed endpoint names one entry and one exit node. Existing
		// pool-based rows remain readable so already-issued endpoints keep working.
		//
		// The chain holds no keys. Under l4_dnat the relay never decrypts, so
		// the client authenticates against the exit's own Reality keys /
		// certificate / WireGuard peer - which is precisely what makes a chain
		// protocol-agnostic: swapping an exit from VLESS to Hysteria2 changes
		// transport and exit_port here and nothing on the relay.
		//
		// transport exists because that swap changes which L4 protocol must be
		// forwarded (Reality is TCP, Hysteria2 and WireGuard are UDP), and
		// exit_port because one exit can serve several protocols at once, each
		// needing its own chain.
		//
		// A direct chain has neither entry_node_id nor relay_pool_id and no entry port.
		//
		// traffic_multiplier overrides the exit node's factor when a relayed
		// path costs differently from the direct one; NULL means inherit.
		`CREATE TABLE IF NOT EXISTS node_chains (
			id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name               TEXT NOT NULL,
			entry_node_id      UUID REFERENCES nodes(id) ON DELETE RESTRICT,
			relay_pool_id      UUID REFERENCES node_groups(id) ON DELETE RESTRICT,
			exit_node_id       UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			exit_port          INT NOT NULL CHECK (exit_port BETWEEN 1 AND 65535),
			entry_host         TEXT NOT NULL DEFAULT '',
			entry_port         INT CHECK (entry_port BETWEEN 1 AND 65535),
			transport          TEXT NOT NULL DEFAULT 'tcp'
			                   CHECK (transport IN ('tcp','udp','tcp_udp')),
			mode               TEXT NOT NULL DEFAULT 'l4_dnat'
			                   CHECK (mode IN ('l4_dnat','wg_tunnel','xray_chain')),
			priority           INT NOT NULL DEFAULT 0,
			enabled            BOOLEAN NOT NULL DEFAULT true,
			health             TEXT NOT NULL DEFAULT 'unknown'
			                   CHECK (health IN ('unknown','healthy','unhealthy')),
			last_probe_at      TIMESTAMPTZ,
			probe_rtt_ms       INT,
			traffic_multiplier NUMERIC(5,2)
			                   CHECK (traffic_multiplier IS NULL
			                          OR (traffic_multiplier > 0 AND traffic_multiplier <= 100)),
			created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chain_entry_set CHECK (
				(entry_node_id IS NULL AND relay_pool_id IS NULL AND entry_port IS NULL)
				OR (num_nonnulls(entry_node_id, relay_pool_id) = 1 AND entry_port IS NOT NULL)
			),
			CONSTRAINT chain_relay_entry_host CHECK (
				(entry_node_id IS NULL AND relay_pool_id IS NULL) OR BTRIM(entry_host) <> ''
			),
			CONSTRAINT chain_no_self_entry CHECK (entry_node_id IS NULL OR entry_node_id <> exit_node_id)
		)`,
		// Converts a node_chains created by the first cut of this feature, which
		// keyed the relay side to one node. CREATE TABLE IF NOT EXISTS above is a
		// no-op on such a database, so without this the indexes below fail on a
		// missing relay_pool_id. Dropping rather than migrating the relayed rows
		// is safe because a single-relay chain has no pool to belong to and the
		// direct rows - the ones subscriptions depend on - are keyed by
		// exit_node_id and survive untouched.
		`DO $$ BEGIN
		   IF EXISTS (
		     SELECT 1 FROM information_schema.columns
		     WHERE table_name = 'node_chains' AND column_name = 'relay_node_id'
		   ) THEN
		     DELETE FROM node_chains WHERE relay_node_id IS NOT NULL;
		     ALTER TABLE node_chains DROP CONSTRAINT IF EXISTS chain_no_self;
		     ALTER TABLE node_chains DROP CONSTRAINT IF EXISTS chain_entry_port_set;
		     DROP INDEX IF EXISTS idx_node_chains_relay_entry;
		     DROP INDEX IF EXISTS idx_node_chains_relay_entry_both;
		     DROP INDEX IF EXISTS idx_node_chains_relay;
		     ALTER TABLE node_chains DROP COLUMN relay_node_id;
		     ALTER TABLE node_chains
		       ADD COLUMN relay_pool_id UUID REFERENCES node_groups(id) ON DELETE RESTRICT;
		     ALTER TABLE node_chains ADD CONSTRAINT chain_entry_set CHECK (
		       (relay_pool_id IS NULL AND entry_port IS NULL)
		       OR (relay_pool_id IS NOT NULL AND entry_port IS NOT NULL)
		     );
		   END IF;
		 END $$`,
		`ALTER TABLE node_chains ADD COLUMN IF NOT EXISTS entry_host TEXT NOT NULL DEFAULT ''`,
		// New links select one entry node directly. Existing pool links are kept
		// intact so their already-issued endpoints and membership still work.
		`ALTER TABLE node_chains ADD COLUMN IF NOT EXISTS entry_node_id UUID REFERENCES nodes(id) ON DELETE RESTRICT`,
		`ALTER TABLE node_chains DROP CONSTRAINT IF EXISTS chain_entry_set`,
		`ALTER TABLE node_chains ADD CONSTRAINT chain_entry_set CHECK (
			(entry_node_id IS NULL AND relay_pool_id IS NULL AND entry_port IS NULL)
			OR (num_nonnulls(entry_node_id, relay_pool_id) = 1 AND entry_port IS NOT NULL)
		)`,
		`ALTER TABLE node_chains DROP CONSTRAINT IF EXISTS chain_relay_entry_host`,
		`DO $$ BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'chain_relay_entry_host'
			) THEN
				ALTER TABLE node_chains ADD CONSTRAINT chain_relay_entry_host
					CHECK ((entry_node_id IS NULL AND relay_pool_id IS NULL) OR BTRIM(entry_host) <> '');
			END IF;
		END $$`,
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chain_no_self_entry') THEN
				ALTER TABLE node_chains ADD CONSTRAINT chain_no_self_entry
					CHECK (entry_node_id IS NULL OR entry_node_id <> exit_node_id);
			END IF;
		END $$`,
		// entry_port claims are fleet-wide rather than per relay. A chain's rule
		// is replicated to every relay in its pool and pools can be re-pointed,
		// so a port that is free on one relay but taken on another would make
		// replication order decide whether a chain works. TCP and UDP may share
		// a number, while tcp_udp participates in both unique claim sets.
		//
		// Abort before changing indexes when the old, weaker indexes allowed an
		// overlapping tcp_udp claim. Operators must resolve those rows explicitly;
		// silently deleting or rewriting a chain would change client endpoints.
		`DO $$ DECLARE duplicate_exit UUID; BEGIN
			SELECT exit_node_id INTO duplicate_exit
			FROM node_chains
			WHERE relay_pool_id IS NULL AND entry_node_id IS NULL
			GROUP BY exit_node_id HAVING COUNT(*) > 1 LIMIT 1;
			IF duplicate_exit IS NOT NULL THEN
				RAISE EXCEPTION 'node_chains: duplicate direct chains for exit %; resolve legacy rows before migration', duplicate_exit;
			END IF;
		END $$`,
		`DO $$ DECLARE conflicting_port INT; BEGIN
			SELECT entry_port INTO conflicting_port
			FROM node_chains
			WHERE entry_port IS NOT NULL AND transport IN ('tcp','tcp_udp')
			GROUP BY entry_port HAVING COUNT(*) > 1 LIMIT 1;
			IF conflicting_port IS NOT NULL THEN
				RAISE EXCEPTION 'node_chains: entry port % has conflicting TCP claims; resolve legacy rows before migration', conflicting_port;
			END IF;

			SELECT entry_port INTO conflicting_port
			FROM node_chains
			WHERE entry_port IS NOT NULL AND transport IN ('udp','tcp_udp')
			GROUP BY entry_port HAVING COUNT(*) > 1 LIMIT 1;
			IF conflicting_port IS NOT NULL THEN
				RAISE EXCEPTION 'node_chains: entry port % has conflicting UDP claims; resolve legacy rows before migration', conflicting_port;
			END IF;
		END $$`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_node_chains_entry_port_tcp_claim
			ON node_chains(entry_port)
			WHERE entry_port IS NOT NULL AND transport IN ('tcp','tcp_udp')`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_node_chains_entry_port_udp_claim
			ON node_chains(entry_port)
			WHERE entry_port IS NOT NULL AND transport IN ('udp','tcp_udp')`,
		// Keep the old indexes until both stronger indexes exist so a failed
		// migration never leaves a previously protected claim set unguarded.
		`DROP INDEX IF EXISTS idx_node_chains_entry_port`,
		`DROP INDEX IF EXISTS idx_node_chains_entry_port_both`,
		`CREATE INDEX IF NOT EXISTS idx_node_chains_relay_pool ON node_chains(relay_pool_id)
			WHERE relay_pool_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_node_chains_entry_node ON node_chains(entry_node_id)
			WHERE entry_node_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_node_chains_exit ON node_chains(exit_node_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_node_chains_one_direct_per_exit_v2
			ON node_chains(exit_node_id) WHERE relay_pool_id IS NULL AND entry_node_id IS NULL`,
		`DROP INDEX IF EXISTS idx_node_chains_one_direct_per_exit`,

		`CREATE TABLE IF NOT EXISTS node_group_chains (
			node_group_id UUID NOT NULL REFERENCES node_groups(id) ON DELETE CASCADE,
			chain_id      UUID NOT NULL REFERENCES node_chains(id) ON DELETE CASCADE,
			PRIMARY KEY (node_group_id, chain_id)
		)`,

		// Give every existing node the direct chain it already was, then point
		// the groups at those chains. Without this the subscription path - which
		// reads chains, not node_group_nodes - would hand out nothing at all on
		// the first boot after this migration. Guarded by NOT EXISTS so a second
		// run cannot mint a duplicate chain per node.
		//
		// FOR KEY SHARE takes the same row lock the foreign key check itself
		// needs, so a node deleted between this statement reading it and the
		// insert referencing it cannot turn the migration - and therefore API
		// startup - into a foreign key violation.
		`WITH backfill AS (
		   SELECT n.id, n.name, n.port
		   FROM nodes n
		   WHERE NOT EXISTS (
		     SELECT 1 FROM node_chains c
		     WHERE c.exit_node_id = n.id AND c.relay_pool_id IS NULL AND c.entry_node_id IS NULL
		   )
		   FOR KEY SHARE
		 )
		 INSERT INTO node_chains (name, relay_pool_id, exit_node_id, exit_port, transport)
		 SELECT b.name, NULL, b.id, b.port, 'tcp_udp' FROM backfill b`,
		`INSERT INTO node_group_chains (node_group_id, chain_id)
		 SELECT ngn.node_group_id, c.id
		 FROM node_group_nodes ngn
		 JOIN node_chains c ON c.exit_node_id = ngn.node_id AND c.relay_pool_id IS NULL AND c.entry_node_id IS NULL
		 ON CONFLICT DO NOTHING`,

		// PHS-027: a shared ordered pool of hosts through which the existing,
		// host-agnostic subscription route is served. Health is a last-observed
		// server-side diagnostic only; it is never an automatic visibility gate.
		`CREATE TABLE IF NOT EXISTS subscription_domains (
			id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			domain                TEXT NOT NULL UNIQUE,
			is_enabled            BOOLEAN NOT NULL DEFAULT true,
			is_public             BOOLEAN NOT NULL DEFAULT true,
			sort_order            INT NOT NULL DEFAULT 0,
			is_default            BOOLEAN NOT NULL DEFAULT false,
			dns_resolved          BOOLEAN NOT NULL DEFAULT false,
			dns_addresses         TEXT[] NOT NULL DEFAULT '{}',
			dns_error             TEXT NOT NULL DEFAULT '',
			tls_reachable         BOOLEAN NOT NULL DEFAULT false,
			tls_error             TEXT NOT NULL DEFAULT '',
			cert_expires_at       TIMESTAMPTZ,
			last_health_check_at  TIMESTAMPTZ,
			created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CHECK (BTRIM(domain) <> '')
		)`,
		// Normalise a partially deployed early version before adding the unique
		// default constraint. Existing operators keep the best ordered default;
		// a pool with no default gains its first ordered row. A zero-row pool is
		// naturally the sole exception because there is nothing to select.
		`WITH ranked_defaults AS (
			SELECT id, ROW_NUMBER() OVER (ORDER BY sort_order, created_at, id) AS rank
			FROM subscription_domains WHERE is_default
		)
		UPDATE subscription_domains d SET is_default = false
		FROM ranked_defaults r WHERE d.id = r.id AND r.rank > 1`,
		`UPDATE subscription_domains SET is_default = true
		 WHERE id = (SELECT id FROM subscription_domains ORDER BY sort_order, created_at, id LIMIT 1)
		   AND NOT EXISTS (SELECT 1 FROM subscription_domains WHERE is_default)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_domains_one_default
			ON subscription_domains(is_default) WHERE is_default`,
		`CREATE INDEX IF NOT EXISTS idx_subscription_domains_public_order
			ON subscription_domains(is_enabled, is_public, sort_order, created_at)
			WHERE is_enabled AND is_public`,
	}
	migrations = append(migrations, managedEndpointMigrations...)
	migrations = append(migrations, deviceBandwidthSchema)
	migrations = append(migrations, hwidSchemaMigrations...)
	migrations = append(migrations, uuidEvictionSchema)

	for _, m := range migrations {
		if _, err := conn.Exec(ctx, m); err != nil {
			return fmt.Errorf("running migration %q: %w", m, err)
		}
	}
	if err := backfillRealitySNI(ctx, conn); err != nil {
		return fmt.Errorf("backfilling Reality SNI: %w", err)
	}

	return nil
}
