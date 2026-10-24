package database

// Reset tickets let a user zero their own monthly data usage once per ticket.
// A grant records one administrator action (to one user, everyone on a plan,
// or every account) and owns the tickets it issued, so the admin history can
// show how many were handed out and how many have been spent.
var resetTicketSchema = []string{
	`CREATE TABLE IF NOT EXISTS reset_ticket_grants (
		id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name              TEXT NOT NULL CHECK (BTRIM(name) <> ''),
		target_type       TEXT NOT NULL CHECK (target_type IN ('user', 'plan', 'all')),
		target_user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
		target_plan_id    UUID REFERENCES plans(id) ON DELETE SET NULL,
		expires_at        TIMESTAMPTZ NOT NULL,
		granted_count     INT NOT NULL DEFAULT 0,
		granted_by_id     TEXT NOT NULL DEFAULT '',
		granted_by_label  TEXT NOT NULL DEFAULT '',
		created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS reset_tickets (
		id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		grant_id     UUID NOT NULL REFERENCES reset_ticket_grants(id) ON DELETE CASCADE,
		user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name         TEXT NOT NULL,
		granted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		expires_at   TIMESTAMPTZ NOT NULL,
		used_at      TIMESTAMPTZ,
		reset_bytes  BIGINT,
		CHECK (expires_at > granted_at)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_reset_tickets_user_usable
		ON reset_tickets(user_id, expires_at) WHERE used_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS idx_reset_tickets_grant ON reset_tickets(grant_id)`,
}
