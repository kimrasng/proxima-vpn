package database

// Bytes each relayed route forwarded through an Entry, as measured by the
// Entry's nftables counters, summed per hour. Kept per Entry so a legacy pool
// route can be attributed to the member that carried it. Rows follow their
// chain and node on delete; the retention sweep drops old hours.
var routeTrafficSchema = []string{
	`CREATE TABLE IF NOT EXISTS route_traffic_hourly (
		chain_id      UUID NOT NULL REFERENCES node_chains(id) ON DELETE CASCADE,
		entry_node_id UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		bucket        TIMESTAMPTZ NOT NULL,
		up_bytes      BIGINT NOT NULL DEFAULT 0 CHECK (up_bytes >= 0),
		down_bytes    BIGINT NOT NULL DEFAULT 0 CHECK (down_bytes >= 0),
		PRIMARY KEY (chain_id, entry_node_id, bucket)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_route_traffic_hourly_bucket ON route_traffic_hourly(bucket)`,
}
