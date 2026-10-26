package database

// adminAccountSchema turns the single admin into a team: owners manage other
// admins, and every admin can be made responsible for support tickets.
//
// role is added with DEFAULT 'owner' so every admin that existed before this
// migration keeps full control; the default is then lowered to 'admin' so new
// rows are least-privileged unless an owner says otherwise.
var adminAccountSchema = []string{
	`ALTER TABLE admins ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'owner'`,
	`ALTER TABLE admins ALTER COLUMN role SET DEFAULT 'admin'`,
	`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'admins_role_check') THEN
			ALTER TABLE admins ADD CONSTRAINT admins_role_check CHECK (role IN ('owner', 'admin'));
		END IF;
	END $$`,
	`ALTER TABLE admins ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE admins ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT true`,
	`ALTER TABLE admins ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ`,
	// Admin JWTs issued before this instant are rejected, so a password change
	// or reset signs every other session out.
	`ALTER TABLE admins ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ NOT NULL DEFAULT to_timestamp(0)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_admins_email_lower ON admins (lower(email))`,
	`ALTER TABLE support_tickets ADD COLUMN IF NOT EXISTS assigned_admin_id UUID REFERENCES admins(id) ON DELETE SET NULL`,
	`CREATE INDEX IF NOT EXISTS idx_support_tickets_assignee ON support_tickets(assigned_admin_id, updated_at DESC)`,
}
