package database

var managedEndpointMigrations = []string{
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS reality_client_sni TEXT`,
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS reality_sni_source TEXT`,
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS reality_sni_status TEXT`,
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS reality_sni_error_code TEXT`,
	`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'nodes'::regclass AND conname = 'nodes_reality_sni_source_check') THEN
			ALTER TABLE nodes ADD CONSTRAINT nodes_reality_sni_source_check
				CHECK (reality_sni_source IS NULL OR reality_sni_source IN ('backfill', 'admin'));
		END IF;
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'nodes'::regclass AND conname = 'nodes_reality_sni_status_check') THEN
			ALTER TABLE nodes ADD CONSTRAINT nodes_reality_sni_status_check
				CHECK (reality_sni_status IS NULL OR reality_sni_status IN ('valid', 'conflict', 'not_applicable'));
		END IF;
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'nodes'::regclass AND conname = 'nodes_reality_sni_error_code_check') THEN
			ALTER TABLE nodes ADD CONSTRAINT nodes_reality_sni_error_code_check
				CHECK (reality_sni_error_code IS NULL OR reality_sni_error_code IN ('no_common_name', 'invalid_sni', 'listener_mismatch', 'not_reality'));
		END IF;
		ALTER TABLE nodes DROP CONSTRAINT IF EXISTS nodes_reality_sni_consistency_check;
		UPDATE nodes SET reality_sni_source = NULL
			WHERE reality_sni_status = 'not_applicable'
				AND reality_client_sni IS NULL AND reality_sni_source IS NOT NULL;
		ALTER TABLE nodes ADD CONSTRAINT nodes_reality_sni_consistency_check CHECK (
			(reality_sni_status IS NULL AND reality_client_sni IS NULL AND reality_sni_source IS NULL AND reality_sni_error_code IS NULL)
			OR (reality_sni_status = 'valid' AND reality_sni_source IS NOT NULL
				AND reality_client_sni IS NOT NULL AND BTRIM(reality_client_sni) <> '' AND reality_sni_error_code IS NULL)
			OR (reality_sni_status = 'conflict' AND reality_sni_source IS NOT NULL
				AND (reality_client_sni IS NULL OR reality_sni_error_code IS NOT NULL))
			OR (reality_sni_status = 'not_applicable' AND reality_sni_error_code IS NULL
				AND ((reality_client_sni IS NULL AND reality_sni_source IS NULL)
					OR (reality_client_sni IS NOT NULL AND BTRIM(reality_client_sni) <> ''
						AND reality_sni_source IS NOT NULL)))
		);
	END $$`,
	`CREATE TABLE IF NOT EXISTS managed_entry_dns (
		id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		owner_node_id        UUID NOT NULL UNIQUE,
		node_id              UUID UNIQUE REFERENCES nodes(id) ON DELETE SET NULL,
		hostname             TEXT UNIQUE CHECK (hostname IS NULL OR BTRIM(hostname) <> ''),
		desired_ipv4         INET CHECK (desired_ipv4 IS NULL OR (family(desired_ipv4) = 4 AND masklen(desired_ipv4) = 32 AND host(desired_ipv4) <> '0.0.0.0')),
		observed_ipv4        INET CHECK (observed_ipv4 IS NULL OR (family(observed_ipv4) = 4 AND masklen(observed_ipv4) = 32 AND host(observed_ipv4) <> '0.0.0.0')),
		desired_action       TEXT NOT NULL DEFAULT 'present' CHECK (desired_action IN ('present', 'delete')),
		dns_status           TEXT CHECK (dns_status IS NULL OR dns_status IN ('pending', 'ready', 'conflict', 'error', 'deleting', 'deleted')),
		observed_at          TIMESTAMPTZ,
		error_code           TEXT CHECK (error_code IS NULL OR error_code IN ('provider_unavailable', 'provider_rejected', 'ownership_conflict', 'invalid_configuration', 'dns_mismatch')),
		cleanup_requested_at TIMESTAMPTZ,
		cloudflare_zone_id   TEXT,
		ownership_marker     TEXT,
		provider_record_id   TEXT,
		generation           BIGINT NOT NULL DEFAULT 1,
		retry_count          INTEGER NOT NULL DEFAULT 0,
		next_attempt_at      TIMESTAMPTZ,
		created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`ALTER TABLE managed_entry_dns ALTER COLUMN desired_action SET DEFAULT 'present'`,
	`CREATE OR REPLACE FUNCTION managed_entry_dns_preserve_owner() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.owner_node_id IS DISTINCT FROM OLD.owner_node_id THEN
			RAISE EXCEPTION 'managed_entry_dns owner_node_id is immutable' USING ERRCODE = '23514';
		END IF;
		RETURN NEW;
	END $$`,
	`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'managed_entry_dns'::regclass AND tgname = 'managed_entry_dns_preserve_owner') THEN
			CREATE TRIGGER managed_entry_dns_preserve_owner BEFORE UPDATE ON managed_entry_dns
			FOR EACH ROW EXECUTE FUNCTION managed_entry_dns_preserve_owner();
		END IF;
	END $$`,
	`ALTER TABLE managed_entry_dns ADD COLUMN IF NOT EXISTS cloudflare_zone_id TEXT`,
	`ALTER TABLE managed_entry_dns ADD COLUMN IF NOT EXISTS ownership_marker TEXT`,
	`ALTER TABLE managed_entry_dns ADD COLUMN IF NOT EXISTS provider_record_id TEXT`,
	`ALTER TABLE managed_entry_dns ADD COLUMN IF NOT EXISTS generation BIGINT NOT NULL DEFAULT 1`,
	`ALTER TABLE managed_entry_dns ADD COLUMN IF NOT EXISTS retry_count INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE managed_entry_dns ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ`,
	`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_entry_dns'::regclass AND conname = 'managed_entry_dns_generation_positive') THEN
			ALTER TABLE managed_entry_dns ADD CONSTRAINT managed_entry_dns_generation_positive CHECK (generation > 0);
		END IF;
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'managed_entry_dns'::regclass AND conname = 'managed_entry_dns_retry_count_nonnegative') THEN
			ALTER TABLE managed_entry_dns ADD CONSTRAINT managed_entry_dns_retry_count_nonnegative CHECK (retry_count >= 0);
		END IF;
	END $$`,
	`CREATE INDEX IF NOT EXISTS idx_managed_entry_dns_due_work
		ON managed_entry_dns(next_attempt_at, id) WHERE next_attempt_at IS NOT NULL`,
}
