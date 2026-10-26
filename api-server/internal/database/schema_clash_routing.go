package database

// clashRoutingSchema backs the admin-editable Clash rule-based routing.
//
// tags are free-form node labels; a routing group can take every node that
// carries a tag, so tagging a node is how it joins groups such as Korean
// residential exits.
//
// clash_routing_profile is a singleton: no row means the built-in default
// profile (subscription.DefaultRoutingProfile) is served.
var clashRoutingSchema = []string{
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}'`,
	`CREATE INDEX IF NOT EXISTS idx_nodes_tags ON nodes USING GIN (tags)`,
	`CREATE TABLE IF NOT EXISTS clash_routing_profile (
		id         SMALLINT PRIMARY KEY CHECK (id = 1),
		document   JSONB NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
}
