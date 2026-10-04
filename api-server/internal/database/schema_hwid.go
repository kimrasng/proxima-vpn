package database

// HWID identities are keyed fingerprints, never raw client headers. NULL keeps
// pre-existing, manually registered devices independent of this registration cap.
var hwidSchemaMigrations = []string{
	`ALTER TABLE devices ADD COLUMN IF NOT EXISTS hwid_fingerprint TEXT`,
	`ALTER TABLE devices ADD COLUMN IF NOT EXISTS first_subscription_at TIMESTAMPTZ`,
	`ALTER TABLE devices ADD COLUMN IF NOT EXISTS last_subscription_at TIMESTAMPTZ`,
	`ALTER TABLE devices ADD COLUMN IF NOT EXISTS last_online_transition_at TIMESTAMPTZ`,
	`ALTER TABLE devices ADD COLUMN IF NOT EXISTS retired_at TIMESTAMPTZ`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_user_hwid ON devices(user_id, hwid_fingerprint)
	 WHERE hwid_fingerprint IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS idx_devices_active_hwid ON devices(user_id)
	 WHERE hwid_fingerprint IS NOT NULL AND retired_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS idx_devices_hwid_refresh ON devices(last_subscription_at)
	 WHERE hwid_fingerprint IS NOT NULL AND retired_at IS NULL`,
	// NULL means the initial product default. This setting is independent of
	// plans.max_devices and plans.max_concurrent; a future plan override can
	// replace it without changing existing plans or device eligibility.
	`INSERT INTO settings (key, value) VALUES ('hwid_registration_cap', '10') ON CONFLICT (key) DO NOTHING`,
	// Kept outside public/admin settings responses; generated once under the
	// migration lock and shared across API replicas and restarts.
	`CREATE TABLE IF NOT EXISTS hwid_secrets (
	 id BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
	 pepper TEXT NOT NULL
	)`,
	`INSERT INTO hwid_secrets (id, pepper) VALUES (true, encode(gen_random_bytes(32), 'hex'))
	 ON CONFLICT (id) DO NOTHING`,
}
