package database

// Guides are admin-authored help documents shown in the user portal. The
// source (Markdown or HTML) is kept for editing; rendered_html is the
// sanitized output the portal displays, produced on the server at save time.
var guideSchema = []string{
	`CREATE TABLE IF NOT EXISTS guide_categories (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name        TEXT NOT NULL CHECK (BTRIM(name) <> ''),
		sort_order  INT NOT NULL DEFAULT 0,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS guides (
		id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		category_id    UUID REFERENCES guide_categories(id) ON DELETE SET NULL,
		title          TEXT NOT NULL CHECK (BTRIM(title) <> ''),
		summary        TEXT NOT NULL DEFAULT '',
		format         TEXT NOT NULL CHECK (format IN ('markdown', 'html')),
		content        TEXT NOT NULL DEFAULT '',
		rendered_html  TEXT NOT NULL DEFAULT '',
		is_published   BOOLEAN NOT NULL DEFAULT false,
		sort_order     INT NOT NULL DEFAULT 0,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_guides_category ON guides(category_id, sort_order)`,
}
