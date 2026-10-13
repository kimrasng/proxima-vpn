package services

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

const maxRelayAdmissionEntries = 256

// ResolveRelayAdmissionSource proves a TCP peer's original source using only
// current forwarding topology and fresh, authenticated Entry flow snapshots.
// It does not authorize a credential or reserve an address. Exit addresses come
// exclusively from host(nodes.ip), not from the caller or a flow report.
func ResolveRelayAdmissionSource(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, exitID, peerIP string, peerPort, localPort int) (string, error) {
	exit, err := uuid.Parse(exitID)
	peer, peerErr := relayAdmissionAddress(peerIP)
	if err != nil || peerErr != nil || nodeprov.ValidatePort(peerPort) != nil || nodeprov.ValidatePort(localPort) != nil {
		return "", fmt.Errorf("invalid relay admission tuple")
	}
	if db == nil || rdb == nil {
		return "", fmt.Errorf("relay admission proof storage unavailable")
	}
	var exitIP string
	if err := db.QueryRow(ctx, `SELECT host(ip) FROM nodes WHERE id=$1 AND role IN ('exit','both')`, exit).Scan(&exitIP); err != nil {
		return "", fmt.Errorf("read registered Exit: %w", err)
	}
	exitAddr, err := relayAdmissionAddress(exitIP)
	if err != nil {
		return "", fmt.Errorf("invalid registered Exit address: %w", err)
	}
	// Match the same explicit-entry/legacy-pool precedence as RelayRulesForNode.
	rows, err := db.Query(ctx, `SELECT DISTINCT n.id::text
  FROM nodes n JOIN node_chains c ON
   (c.entry_node_id=n.id OR (c.entry_node_id IS NULL AND EXISTS (
    SELECT 1 FROM node_group_nodes ngn WHERE ngn.node_group_id=c.relay_pool_id AND ngn.node_id=n.id)))
  WHERE n.role IN ('relay','both') AND c.exit_node_id=$1
   AND c.enabled AND c.mode='l4_dnat' AND c.exit_port=$2
   AND c.transport IN ('tcp','tcp_udp')
  ORDER BY n.id::text LIMIT $3`, exit, localPort, maxRelayAdmissionEntries+1)
	if err != nil {
		return "", fmt.Errorf("read relay admission topology: %w", err)
	}
	entries := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		entries = append(entries, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(entries) > maxRelayAdmissionEntries {
		return "", fmt.Errorf("relay admission topology exceeds proof bound")
	}
	source := ""
	for _, id := range entries {
		// Reject persistent or overlong snapshots too; receipt IDs alone are not
		// freshness evidence. Do not refresh TTL or otherwise mutate observations.
		ttl, err := rdb.PTTL(ctx, "node:"+id+":relay_flows").Result()
		if err != nil {
			return "", fmt.Errorf("read relay report freshness: %w", err)
		}
		if ttl <= 0 || ttl > RelayFlowFreshness {
			continue
		}
		report, reportID, err := ReadRelayFlowsWithID(ctx, rdb, id)
		if err != nil || !report.Known || reportID == "" {
			continue
		}
		for _, flow := range report.Flows {
			fp, _ := relayAdmissionAddress(flow.PeerIP)
			fe, _ := relayAdmissionAddress(flow.ExitIP)
			if fp != peer || fe != exitAddr || flow.PeerPort != peerPort || flow.ExitPort != localPort {
				continue
			}
			client, err := relayAdmissionAddress(flow.ClientIP)
			if err != nil {
				return "", fmt.Errorf("invalid original relay source: %w", err)
			}
			if source != "" && source != client.String() {
				return "", fmt.Errorf("%w: ambiguous flow tuple", ErrUnknownSourceAttribution)
			}
			source = client.String()
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if source == "" {
		return "", ErrUnknownSourceAttribution
	}
	return source, nil
}

func relayAdmissionAddress(raw string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, err
	}
	addr = addr.Unmap()
	if addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() || (!addr.IsGlobalUnicast() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast()) {
		return netip.Addr{}, fmt.Errorf("invalid source address %q", raw)
	}
	return addr, nil
}
