package database

// oauthSchema backs the OAuth 2.0 provider that lets first- and third-party
// client apps act on a user's account with the scopes the user approved.
//
// Every bearer credential (client secret, authorization code, access token,
// refresh token) is stored only as a SHA-256 hash, so a database read never
// yields a usable credential.
var oauthSchema = []string{
	`CREATE TABLE IF NOT EXISTS oauth_clients (
		id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		client_id           TEXT NOT NULL UNIQUE,
		client_secret_hash  TEXT,
		name                TEXT NOT NULL,
		description         TEXT NOT NULL DEFAULT '',
		client_type         TEXT NOT NULL DEFAULT 'confidential'
		                    CHECK (client_type IN ('confidential', 'public')),
		redirect_uris       TEXT[] NOT NULL DEFAULT '{}',
		allowed_scopes      TEXT[] NOT NULL DEFAULT '{}',
		is_active           BOOLEAN NOT NULL DEFAULT true,
		secret_rotated_at   TIMESTAMPTZ,
		created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
		code_hash              TEXT PRIMARY KEY,
		client_id              UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
		user_id                UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		redirect_uri           TEXT NOT NULL,
		scopes                 TEXT[] NOT NULL,
		code_challenge         TEXT NOT NULL DEFAULT '',
		code_challenge_method  TEXT NOT NULL DEFAULT '',
		expires_at             TIMESTAMPTZ NOT NULL,
		used_at                TIMESTAMPTZ,
		created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_codes_expires ON oauth_authorization_codes(expires_at)`,
	// One row per issued access/refresh pair. code_hash links the pair to the
	// authorization code that minted it (directly or through refresh), so a
	// replayed code can revoke everything descended from it.
	`CREATE TABLE IF NOT EXISTS oauth_tokens (
		id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		client_id           UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
		user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		access_token_hash   TEXT NOT NULL UNIQUE,
		refresh_token_hash  TEXT UNIQUE,
		scopes              TEXT[] NOT NULL,
		code_hash           TEXT NOT NULL DEFAULT '',
		access_expires_at   TIMESTAMPTZ NOT NULL,
		refresh_expires_at  TIMESTAMPTZ,
		revoked_at          TIMESTAMPTZ,
		created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_used_at        TIMESTAMPTZ
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_tokens_user_client ON oauth_tokens(user_id, client_id)`,
	`CREATE INDEX IF NOT EXISTS idx_oauth_tokens_code ON oauth_tokens(code_hash) WHERE code_hash <> ''`,
	// The user's standing consent per app; revoking it revokes every token.
	`CREATE TABLE IF NOT EXISTS oauth_grants (
		user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		client_id   UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
		scopes      TEXT[] NOT NULL,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (user_id, client_id)
	)`,
}
