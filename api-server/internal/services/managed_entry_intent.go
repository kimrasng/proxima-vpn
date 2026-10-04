package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// ManagedEntryDNSIntentConfig carries no provider credential into registration.
type ManagedEntryDNSIntentConfig struct {
	Enabled    bool
	ZoneID     string
	BaseDomain string
}

type managedEntryIntentRow struct {
	id                           string
	hostname, zone, marker, ipv4 *string
	action, status               string
}

func (cfg ManagedEntryDNSIntentConfig) base() string {
	if !cfg.Enabled || len(cfg.ZoneID) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(cfg.ZoneID); err != nil {
		return ""
	}
	base, err := reality.NormalizeHostname(cfg.BaseDomain)
	if err != nil || len(base) > 220 {
		return ""
	}
	return base.String()
}

// EnsureManagedEntryDNSIntent writes desired state only; provider work is asynchronous.
func EnsureManagedEntryDNSIntent(ctx context.Context, tx pgx.Tx, locked *LockedRealityNode, cfg ManagedEntryDNSIntentConfig) error {
	if err := locked.check(tx); err != nil {
		return err
	}
	var role nodeprov.Role
	var ip string
	if err := tx.QueryRow(ctx, `SELECT role, host(ip) FROM nodes WHERE id=$1`, locked.id).Scan(&role, &ip); err != nil {
		return fmt.Errorf("read locked entry node: %w", err)
	}
	if !role.Forwards() {
		return nil
	}
	addr, parseErr := netip.ParseAddr(ip)
	base := cfg.base()
	valid := base != "" && parseErr == nil && addr.Is4() && !addr.IsUnspecified()

	var row managedEntryIntentRow
	created := false
	err := tx.QueryRow(ctx, `SELECT id::text, hostname, cloudflare_zone_id, ownership_marker, host(desired_ipv4), desired_action, COALESCE(dns_status,'') FROM managed_entry_dns WHERE owner_node_id=$1 FOR UPDATE`, locked.id).Scan(&row.id, &row.hostname, &row.zone, &row.marker, &row.ipv4, &row.action, &row.status)
	if errors.Is(err, pgx.ErrNoRows) {
		created = true
		err = tx.QueryRow(ctx, `INSERT INTO managed_entry_dns (owner_node_id,node_id,dns_status,error_code) VALUES ($1,$1,'error','invalid_configuration') RETURNING id::text`, locked.id).Scan(&row.id)
		if err != nil {
			return fmt.Errorf("create entry DNS intent: %w", err)
		}
		row.action = "present"
		row.status = "error"
	} else if err != nil {
		return fmt.Errorf("read entry DNS intent: %w", err)
	}
	if row.action == "delete" {
		return nil
	}
	if row.zone != nil && *row.zone != cfg.ZoneID {
		valid = false
	}
	if row.hostname != nil && (base == "" || !strings.HasSuffix(strings.ToLower(*row.hostname), "."+base)) {
		valid = false
	}
	if !valid {
		if _, err := tx.Exec(ctx, `UPDATE managed_entry_dns SET dns_status='error',error_code='invalid_configuration',next_attempt_at=NULL,updated_at=NOW() WHERE id=$1`, row.id); err != nil {
			return fmt.Errorf("mark entry DNS configuration: %w", err)
		}
		return nil
	}
	if row.hostname == nil {
		name, err := allocateManagedEntryHostname(ctx, tx, row.id, base)
		if err != nil {
			return err
		}
		row.hostname = &name
	}
	changed := row.ipv4 == nil || *row.ipv4 != addr.String()
	if _, err := tx.Exec(ctx, `UPDATE managed_entry_dns SET
		cloudflare_zone_id=COALESCE(cloudflare_zone_id,$2),
		ownership_marker=COALESCE(ownership_marker,'proxima-entry:' || id::text),
		desired_ipv4=$3::inet,
		generation=generation + CASE WHEN NOT $5 AND desired_ipv4 IS DISTINCT FROM $3::inet THEN 1 ELSE 0 END,
		dns_status=CASE WHEN $4 OR error_code='invalid_configuration' THEN 'pending' ELSE dns_status END,
		error_code=CASE WHEN $4 OR error_code='invalid_configuration' THEN NULL ELSE error_code END,
		next_attempt_at=CASE WHEN $4 OR error_code='invalid_configuration' THEN NOW() ELSE next_attempt_at END,
		updated_at=NOW() WHERE id=$1`, row.id, cfg.ZoneID, addr.String(), changed, created); err != nil {
		return fmt.Errorf("refresh entry DNS intent: %w", err)
	}
	return nil
}

func allocateManagedEntryHostname(ctx context.Context, tx pgx.Tx, rowID, base string) (string, error) {
	for range 5 {
		var entropy [16]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return "", fmt.Errorf("generate entry hostname: %w", err)
		}
		name := hex.EncodeToString(entropy[:]) + "." + base
		attempt, err := tx.Begin(ctx)
		if err != nil {
			return "", fmt.Errorf("begin entry hostname attempt: %w", err)
		}
		_, err = attempt.Exec(ctx, `UPDATE managed_entry_dns SET hostname=$2 WHERE id=$1`, rowID, name)
		if err == nil {
			err = attempt.Commit(ctx)
		} else {
			_ = attempt.Rollback(ctx)
		}
		if err == nil {
			return name, nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			return "", fmt.Errorf("allocate entry hostname: %w", err)
		}
	}
	return "", errors.New("entry hostname collision retry exhausted")
}
