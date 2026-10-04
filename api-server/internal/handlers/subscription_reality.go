package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// realityApplied requires a fresh agent acknowledgement of the current generated
// config and rechecks the projected listener set after generation. A raced edit
// is withheld rather than combining a stale endpoint with a new digest.
func realityApplied(ctx context.Context, db *pgxpool.Pool, config *services.XrayConfigService, node subscriptionNode) (string, []int, bool) {
	if node.Status != "online" || !node.XrayRunning || !node.HeartbeatFresh ||
		node.RealityStatus != "valid" || node.ConfigHash == "" {
		return "", nil, false
	}
	sni, err := reality.NormalizeHostname(node.RealitySNI)
	if err != nil {
		return "", nil, false
	}
	digest, err := config.GenerateDigest(ctx, node.ID)
	if err != nil || digest.Hash != node.ConfigHash {
		return "", nil, false
	}
	generated, err := config.GenerateConfig(ctx, node.ID)
	if err != nil {
		return "", nil, false
	}
	configHash := sha256.Sum256(generated)
	generatedPorts, accepted := configRealityPorts(generated, sni)
	if hex.EncodeToString(configHash[:]) != digest.Hash || !accepted {
		return "", nil, false
	}
	for _, port := range node.RealityPorts {
		if !slices.Contains(generatedPorts, port) {
			return "", nil, false
		}
	}
	var ports []int
	var currentSNI, status, hash string
	var running, fresh bool
	err = db.QueryRow(ctx, `SELECT
		COALESCE((SELECT array_agg(port ORDER BY port) FILTER (WHERE protocol='vless_reality') FROM inbounds WHERE node_id=n.id AND enabled=true),
		  CASE WHEN NOT EXISTS (SELECT 1 FROM inbounds WHERE node_id=n.id AND enabled=true) THEN ARRAY[n.port]::integer[] ELSE ARRAY[]::integer[] END),
		COALESCE(n.reality_client_sni,''), COALESCE(n.reality_sni_status,''), COALESCE(n.config_hash,''),
		COALESCE(n.xray_running,false), COALESCE(n.status='online' AND n.last_seen >= NOW()-INTERVAL '40 seconds',false)
		FROM nodes n WHERE n.id=$1`, node.ID).Scan(&ports, &currentSNI, &status, &hash, &running, &fresh)
	if err != nil || !running || !fresh || status != "valid" || currentSNI != node.RealitySNI ||
		hash != digest.Hash || !slices.Equal(ports, node.RealityPorts) {
		return "", nil, false
	}
	return sni.String(), generatedPorts, true
}

func configRealityPorts(config []byte, sni reality.Hostname) ([]int, bool) {
	var generated struct {
		Inbounds []struct {
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Stream   struct {
				Security string `json:"security"`
				Reality  struct {
					ServerNames []string `json:"serverNames"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(config, &generated); err != nil {
		return nil, false
	}
	var listeners []reality.Listener
	var ports []int
	for _, inbound := range generated.Inbounds {
		if inbound.Protocol == "vless" && inbound.Stream.Security == "reality" {
			ports = append(ports, inbound.Port)
			listeners = append(listeners, reality.Listener{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: inbound.Stream.Reality.ServerNames})
		}
	}
	result, err := reality.Intersect(listeners)
	return ports, err == nil && result.Status == reality.Valid && slices.Contains(result.Candidates, sni)
}
