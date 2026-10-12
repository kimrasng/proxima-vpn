package services

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// maxRouteTrafficBytes caps one reported delta. It is far beyond what a link
// can forward between two heartbeats, and keeps a misbehaving agent from
// overflowing a bucket.
const maxRouteTrafficBytes = int64(1) << 50

type routeTrafficExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// RecordRouteTraffic adds an Entry's per-route counter deltas to the current
// hour bucket of the chains they belong to.
//
// The agent identifies a route by entry port and protocol only. Entry port
// claims are unique per protocol across the fleet, so together with the
// reporting node they select at most one chain. Enabled state and mode are
// deliberately not filtered: a route disabled since the counters were read
// still carried those bytes. Unattributable entries are dropped.
//
// Malformed entries are skipped rather than rejected: per-route traffic is a
// diagnostic, and failing the heartbeat over it would mark the node offline.
func RecordRouteTraffic(ctx context.Context, db routeTrafficExecer, nodeID string, traffic []nodeprov.RouteTraffic) error {
	if len(traffic) > nodeprov.MaxRouteTrafficEntries {
		log.Printf("node %s reported %d route traffic entries; keeping the first %d", nodeID, len(traffic), nodeprov.MaxRouteTrafficEntries)
		traffic = traffic[:nodeprov.MaxRouteTrafficEntries]
	}
	ports := make([]int32, 0, len(traffic))
	transports := make([]string, 0, len(traffic))
	up := make([]int64, 0, len(traffic))
	down := make([]int64, 0, len(traffic))
	for _, t := range traffic {
		if err := t.Validate(); err != nil {
			log.Printf("node %s route traffic entry skipped: %v", nodeID, err)
			continue
		}
		if t.UpBytes == 0 && t.DownBytes == 0 {
			continue
		}
		ports = append(ports, int32(t.EntryPort))
		transports = append(transports, string(t.Transport))
		up = append(up, min(t.UpBytes, maxRouteTrafficBytes))
		down = append(down, min(t.DownBytes, maxRouteTrafficBytes))
	}
	if len(ports) == 0 {
		return nil
	}
	_, err := db.Exec(ctx,
		`WITH reported AS (
		   SELECT * FROM unnest($2::int[], $3::text[], $4::bigint[], $5::bigint[])
		     AS r(entry_port, transport, up_bytes, down_bytes)
		 ), attributed AS (
		   SELECT c.id AS chain_id, SUM(r.up_bytes) AS up_bytes, SUM(r.down_bytes) AS down_bytes
		   FROM reported r
		   JOIN node_chains c ON c.entry_port = r.entry_port
		     AND (c.transport = r.transport OR c.transport = 'tcp_udp')
		     AND (c.entry_node_id = $1::uuid OR (c.entry_node_id IS NULL AND EXISTS (
		       SELECT 1 FROM node_group_nodes ngn
		       WHERE ngn.node_group_id = c.relay_pool_id AND ngn.node_id = $1::uuid)))
		   GROUP BY c.id
		 )
		 INSERT INTO route_traffic_hourly (chain_id, entry_node_id, bucket, up_bytes, down_bytes)
		 SELECT chain_id, $1::uuid, date_trunc('hour', NOW()),
		        LEAST(up_bytes, 9223372036854775807)::bigint, LEAST(down_bytes, 9223372036854775807)::bigint
		 FROM attributed
		 ON CONFLICT (chain_id, entry_node_id, bucket) DO UPDATE SET
		   up_bytes = LEAST(route_traffic_hourly.up_bytes::numeric + EXCLUDED.up_bytes, 9223372036854775807)::bigint,
		   down_bytes = LEAST(route_traffic_hourly.down_bytes::numeric + EXCLUDED.down_bytes, 9223372036854775807)::bigint`,
		nodeID, ports, transports, up, down,
	)
	if err != nil {
		return fmt.Errorf("record route traffic: %w", err)
	}
	return nil
}

// RouteTrafficWindowHours is how far back the route list sums forwarded
// bytes: the current hour bucket plus the previous 23.
const RouteTrafficWindowHours = 24

// RouteTrafficSummary is what Entries forwarded for one route recently. Bytes
// are wire bytes (IP and transport headers included) as the Entry's kernel
// counted them, so they read slightly above tunnel payload.
type RouteTrafficSummary struct {
	UpBytes   int64 `json:"up_bytes"`
	DownBytes int64 `json:"down_bytes"`
}

type routeTrafficQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// RecentRouteTraffic sums every chain's forwarded bytes over the window.
// Chains without traffic are absent from the result.
func RecentRouteTraffic(ctx context.Context, db routeTrafficQuerier) (map[string]RouteTrafficSummary, error) {
	rows, err := db.Query(ctx,
		`SELECT chain_id::text,
		        LEAST(SUM(up_bytes), 9223372036854775807)::bigint,
		        LEAST(SUM(down_bytes), 9223372036854775807)::bigint
		 FROM route_traffic_hourly
		 WHERE bucket >= date_trunc('hour', NOW()) - make_interval(hours => $1)
		 GROUP BY chain_id`,
		RouteTrafficWindowHours-1,
	)
	if err != nil {
		return nil, fmt.Errorf("read route traffic: %w", err)
	}
	defer rows.Close()
	out := map[string]RouteTrafficSummary{}
	for rows.Next() {
		var id string
		var s RouteTrafficSummary
		if err := rows.Scan(&id, &s.UpBytes, &s.DownBytes); err != nil {
			return nil, fmt.Errorf("scan route traffic: %w", err)
		}
		out[id] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read route traffic: %w", err)
	}
	return out, nil
}
