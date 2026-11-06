package services

import (
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"strconv"
	"strings"
)

func historyQueryPredicate(q ConnectionHistoryQuery) (string, []any, error) {
	args := []any{}
	where := " WHERE true"
	add := func(sql string, arg any) { args = append(args, arg); where += sql + strconv.Itoa(len(args)) }
	for _, id := range []string{q.NodeID, q.UserID, q.Epoch} {
		if id != "" && nodeprov.HistoryUUID(id).Validate() != nil {
			return "", nil, historyError(400, "invalid_filter")
		}
	}
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		return "", nil, historyError(400, "invalid_interval")
	}
	if q.NodeID != "" {
		add(" AND p.node_origin=$", q.NodeID)
	}
	if q.Epoch != "" {
		add(" AND p.epoch=$", q.Epoch)
	}
	if q.UserID != "" {
		add(" AND b.user_id=$", q.UserID)
	}
	if q.TunnelID != "" {
		add(" AND p.payload->>'tunnel_id'=$", q.TunnelID)
	}
	if q.EntityID != "" {
		add(" AND p.entity_id=$", q.EntityID)
	}
	if q.Protocol != "" {
		switch q.Protocol {
		case "vless", "vmess", "trojan", "shadowsocks", "shadowsocks2022", "hysteria2", "wireguard":
		default:
			return "", nil, historyError(400, "invalid_protocol")
		}
		add(" AND p.payload->>'protocol'=$", q.Protocol)
	}
	if q.Status != "" {
		switch q.Status {
		case "observing", "ended", "interrupted":
		default:
			return "", nil, historyError(400, "invalid_status")
		}
		add(" AND p.observation_status=$", q.Status)
	}
	if len(q.Destination) > 1024 || len(q.DestinationPrefix) > 1024 || (q.Destination != "" && q.DestinationPrefix != "") {
		return "", nil, historyError(400, "invalid_destination")
	}
	// The original requested host is authoritative when present. WG and
	// response-only observations use the observed IP, never DNS resolution.
	destination := "coalesce(p.payload->'destination'->>'requested_host',p.payload->'destination'->>'observed_ip')"
	if q.Destination != "" {
		add(" AND "+destination+"=$", q.Destination)
	}
	if q.DestinationPrefix != "" {
		escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q.DestinationPrefix)
		add(" AND "+destination+" LIKE $", escaped+"%")
	}
	// Observation intervals, not arbitrary cumulative-counter interpolation.
	if q.From != nil {
		add(" AND p.started_at >= $", *q.From)
	}
	if q.To != nil {
		add(" AND p.ended_at <= $", *q.To)
	}
	return where, args, nil
}
