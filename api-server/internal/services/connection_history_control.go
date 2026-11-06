package services

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type ConnectionHistoryNodeCAS struct {
	ID               nodeprov.HistoryUUID `json:"id"`
	ExpectedRevision nodeprov.HistoryU64  `json:"expected_revision"`
}
type ConnectionHistoryBulkCommand struct {
	Nodes   []ConnectionHistoryNodeCAS `json:"nodes"`
	Enabled bool                       `json:"enabled"`
}
type ConnectionHistoryProducerStatus struct {
	ProducerID          string                `json:"producer_id"`
	ObserverBootID      *nodeprov.HistoryUUID `json:"observer_boot_id"`
	RuntimeBuild        string                `json:"runtime_build"`
	Capabilities        []string              `json:"capabilities"`
	AppliedRevision     nodeprov.HistoryU64   `json:"applied_revision"`
	LastDurableSequence nodeprov.HistoryU64   `json:"last_durable_sequence"`
	ErrorCode           string                `json:"error_code"`
}
type ConnectionHistoryStatus struct {
	DesiredRevision   nodeprov.HistoryU64               `json:"desired_revision"`
	AppliedRevision   nodeprov.HistoryU64               `json:"applied_revision"`
	CollectionEpoch   *nodeprov.HistoryUUID             `json:"collection_epoch"`
	Enabled           bool                              `json:"enabled"`
	State             string                            `json:"state"`
	AppliedAt         *nodeprov.HistoryTime             `json:"applied_at"`
	LastConfigFetchAt *nodeprov.HistoryTime             `json:"last_config_fetch_at"`
	QueueBytes        nodeprov.HistoryU64               `json:"queue_bytes"`
	DroppedEvents     nodeprov.HistoryU64               `json:"dropped_events"`
	ErrorCode         string                            `json:"error_code"`
	Producers         []ConnectionHistoryProducerStatus `json:"producers"`
}

func historyProducerRuntime(p ConnectionHistoryProducerStatus) string {
	runtime, _, _ := strings.Cut(p.ProducerID, ":")
	return runtime
}
func historyProtocolRuntime(protocol string) string {
	switch protocol {
	case "hysteria2", "wireguard":
		return protocol
	default:
		return "xray"
	}
}
func historyProducerSupported(p ConnectionHistoryProducerStatus, protocol string) bool {
	if p.ErrorCode != "" || p.RuntimeBuild == "" || p.ObserverBootID == nil || !slices.Contains(p.Capabilities, "hot_toggle") {
		return false
	}
	runtime := historyProducerRuntime(p)
	required := []string{"auth_transport", "tcp_flow", "udp_destination"}
	switch protocol {
	case "wireguard":
		if runtime != "wireguard" {
			return false
		}
		required = []string{"activity_window", "inner_ip_bytes"}
	case "hysteria2":
		if runtime != "hysteria2" {
			return false
		}
	case "vless_reality", "vmess_ws", "trojan_tls", "shadowsocks", "shadowsocks2022":
		if runtime != "xray" {
			return false
		}
		if protocol == "vless_reality" || protocol == "vmess_ws" {
			required = append(required, "mux", "xudp")
		}
	default:
		return false
	}
	for _, capability := range required {
		if !slices.Contains(p.Capabilities, capability) {
			return false
		}
	}
	return true
}

// BulkControl locks nodes in UUID order and validates the entire command before
// any policy write. Capability claims come only from fresh authenticated status.
func (s *ConnectionHistoryService) BulkControl(ctx context.Context, cmd ConnectionHistoryBulkCommand) (map[string]ConnectionHistoryConfig, error) {
	if len(cmd.Nodes) == 0 || len(cmd.Nodes) > 1000 {
		return nil, historyError(400, "invalid_nodes")
	}
	nodes := slices.Clone(cmd.Nodes)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for i, n := range nodes {
		if n.ID.Validate() != nil || (i > 0 && n.ID == nodes[i-1].ID) {
			return nil, historyError(400, "invalid_nodes")
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	configs := make(map[string]ConnectionHistoryConfig)
	reasons := make(map[string]string)
	for _, n := range nodes {
		id := string(n.ID)
		if err = ensureHistoryControl(ctx, tx, id); err != nil {
			var domain *ConnectionHistoryError
			if errors.As(err, &domain) && domain.Status == 404 {
				reasons[id] = "node_not_found"
				continue
			}
			return nil, err
		}
		cfg, e := readHistoryConfig(ctx, tx, id)
		if e != nil {
			return nil, e
		}
		configs[id] = cfg
		if cfg.Revision != n.ExpectedRevision || uint64(cfg.Revision) == ^uint64(0) {
			reasons[id] = "revision_conflict"
			continue
		}
		if !cmd.Enabled {
			continue
		}
		var role string
		var raw []byte
		var at *time.Time
		if err = tx.QueryRow(ctx, `SELECT n.role,c.status,c.status_at FROM nodes n JOIN connection_log_controls c ON c.node_origin=n.id WHERE n.id=$1`, id).Scan(&role, &raw, &at); err != nil {
			return nil, err
		}
		if role == "relay" || role == "entry" {
			reasons[id] = "entry_only"
			continue
		}
		if at == nil || at.Before(s.now().Add(-30*time.Second)) {
			reasons[id] = "capabilities_stale"
			continue
		}
		var status ConnectionHistoryStatus
		if err = json.Unmarshal(raw, &status); err != nil {
			return nil, err
		}
		rows, e := tx.Query(ctx, `SELECT DISTINCT protocol FROM inbounds WHERE node_id=$1 AND enabled`, id)
		if e != nil {
			return nil, e
		}
		count := 0
		for rows.Next() {
			var proto string
			if e = rows.Scan(&proto); e != nil {
				rows.Close()
				return nil, e
			}
			count++
			found := false
			allSupported := true
			for _, p := range status.Producers {
				if historyProducerRuntime(p) != historyProtocolRuntime(proto) {
					continue
				}
				found = true
				if !historyProducerSupported(p, proto) {
					allSupported = false
				}
			}
			if !found || !allSupported {
				reasons[id] = "unsupported:" + proto
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if count == 0 {
			reasons[id] = "no_local_suppliers"
		}
	}
	if len(reasons) > 0 {
		return nil, &ConnectionHistoryError{Status: 409, Code: "bulk_conflict", Nodes: reasons}
	}
	for _, n := range nodes {
		id := string(n.ID)
		cfg := configs[id]
		if cfg.Enabled == cmd.Enabled {
			continue
		}
		cfg.Enabled = cmd.Enabled
		cfg.Revision++
		if cfg.Enabled {
			epoch, e := nodeprov.NewHistoryUUID()
			if e != nil {
				return nil, e
			}
			cfg.CollectionEpoch = &epoch
			if _, err = tx.Exec(ctx, `INSERT INTO connection_log_epochs(node_origin,epoch,issued_at) VALUES($1,$2,$3)`, id, string(epoch), s.now()); err != nil {
				return nil, err
			}
		} else if cfg.CollectionEpoch != nil {
			if _, err = tx.Exec(ctx, `UPDATE connection_log_epochs SET closed_at=$3 WHERE node_origin=$1 AND epoch=$2`, id, string(*cfg.CollectionEpoch), s.now()); err != nil {
				return nil, err
			}
		}
		if err = s.writeHistoryRevision(ctx, tx, id, cfg.Enabled, cfg.Revision, cfg.CollectionEpoch); err != nil {
			return nil, err
		}
		configs[id] = cfg
	}
	return configs, tx.Commit(ctx)
}
