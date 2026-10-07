package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

// RealityTargetProbe checks, before anything is persisted, that the camouflage
// target completes the TLS handshake Reality needs. A nil probe skips it.
type RealityTargetProbe func(ctx context.Context, hostname string, port int) error

// ChangeRealityTarget atomically moves a node and every enabled Reality
// listener to one camouflage target. The old API rejected this in both
// directions (node SNI vs. listener server names); here only the final state
// is validated, inside a single transaction under the node lock.
func ChangeRealityTarget(ctx context.Context, tx pgx.Tx, locked *LockedRealityNode, rawHost string, port int, probe RealityTargetProbe) (string, error) {
	if err := locked.check(tx); err != nil {
		return "", err
	}
	name, err := reality.NormalizeHostname(rawHost)
	if err != nil {
		return "", &RealitySNIError{Kind: RealitySNIMalformed, Cause: err}
	}
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return "", &RealitySNIError{Kind: RealitySNIMalformed, Cause: fmt.Errorf("invalid target port %d", port)}
	}
	host := name.String()
	if probe != nil {
		if err := probe(ctx, host, port); err != nil {
			return "", &RealityTargetUnreachableError{Host: host, Port: port, Cause: err}
		}
	}

	rows, err := tx.Query(ctx, `SELECT id::text, settings FROM inbounds WHERE node_id=$1 AND protocol='vless_reality' AND enabled=true ORDER BY created_at, id`, locked.id)
	if err != nil {
		return "", fmt.Errorf("read Reality inbounds: %w", err)
	}
	type update struct {
		id       string
		settings []byte
	}
	var updates []update
	for rows.Next() {
		var id string
		var raw json.RawMessage
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan Reality inbound: %w", err)
		}
		settings := map[string]json.RawMessage{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &settings); err != nil {
				rows.Close()
				return "", fmt.Errorf("decode Reality inbound settings: %w", err)
			}
		}
		// Only the camouflage fields change; keys, short IDs etc. are kept.
		settings["dest"], _ = json.Marshal(net.JoinHostPort(host, strconv.Itoa(port)))
		settings["server_names"], _ = json.Marshal([]string{host})
		encoded, _ := json.Marshal(settings)
		updates = append(updates, update{id: id, settings: encoded})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", fmt.Errorf("iterate Reality inbounds: %w", err)
	}
	for _, u := range updates {
		if _, err := tx.Exec(ctx, `UPDATE inbounds SET settings=$2 WHERE id=$1`, u.id, u.settings); err != nil {
			return "", fmt.Errorf("update Reality inbound: %w", err)
		}
	}

	// Validate the resulting listener set exactly as the generator sees it.
	listeners, err := LoadEffectiveRealityListeners(ctx, tx, locked)
	if err != nil {
		return "", err
	}
	if _, err := intersectServiceListeners(listeners); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET reality_client_sni=$2, reality_sni_source='admin', reality_sni_status='valid', reality_sni_error_code=NULL WHERE id=$1`, locked.id, host); err != nil {
		return "", fmt.Errorf("persist Reality target: %w", err)
	}
	locked.canonical, locked.source = &host, "admin"
	return host, nil
}

// RealityTargetUnreachableError means the pre-flight handshake failed; nothing
// was changed.
type RealityTargetUnreachableError struct {
	Host  string
	Port  int
	Cause error
}

func (e *RealityTargetUnreachableError) Error() string {
	return fmt.Sprintf("Reality target %s:%d failed TLS 1.3/X25519 check: %v", e.Host, e.Port, e.Cause)
}

func (e *RealityTargetUnreachableError) Unwrap() error { return e.Cause }
