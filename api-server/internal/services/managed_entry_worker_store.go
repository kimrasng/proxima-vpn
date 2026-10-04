package services

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/models"
)

var ErrManagedEntryDNSStale = errors.New("managed entry DNS attempt is stale")

type ManagedEntryDNSDue struct {
	ID          string
	OwnerNodeID string
}

type ManagedEntryDNSWorkerStore struct {
	pool *pgxpool.Pool
}

func NewManagedEntryDNSWorkerStore(pool *pgxpool.Pool) *ManagedEntryDNSWorkerStore {
	return &ManagedEntryDNSWorkerStore{pool: pool}
}

func (s *ManagedEntryDNSWorkerStore) ListDue(ctx context.Context, limit int) ([]ManagedEntryDNSDue, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("managed entry DNS due limit out of range: %d", limit)
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,owner_node_id::text FROM managed_entry_dns
		WHERE next_attempt_at<=NOW() ORDER BY next_attempt_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list due managed entry DNS: %w", err)
	}
	defer rows.Close()
	result := make([]ManagedEntryDNSDue, 0, limit)
	for rows.Next() {
		var due ManagedEntryDNSDue
		if err := rows.Scan(&due.ID, &due.OwnerNodeID); err != nil {
			return nil, fmt.Errorf("scan due managed entry DNS: %w", err)
		}
		result = append(result, due)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read due managed entry DNS: %w", err)
	}
	return result, nil
}

// ManagedEntryDNSAttempt owns a pinned Postgres session, never a provider-spanning transaction.
type ManagedEntryDNSAttempt struct {
	mu          sync.Mutex
	conn        *pgxpool.Conn
	ownerNodeID string
	snapshot    models.ManagedEntryDNS
}

func (s *ManagedEntryDNSWorkerStore) TryAcquire(ctx context.Context, ownerNodeID string) (*ManagedEntryDNSAttempt, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire managed entry DNS connection: %w", err)
	}
	var acquired bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1::uuid::text, 29029))`, ownerNodeID).Scan(&acquired)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		closeErr := conn.Hijack().Close(cleanup)
		return nil, errors.Join(fmt.Errorf("try managed entry DNS owner lock: %w", err), closeErr)
	}
	if !acquired {
		conn.Release()
		return nil, nil
	}
	a := &ManagedEntryDNSAttempt{conn: conn, ownerNodeID: ownerNodeID}
	var desired, observed *string
	err = conn.QueryRow(ctx, `SELECT id::text,owner_node_id::text,node_id::text,hostname,
		host(desired_ipv4),host(observed_ipv4),desired_action,dns_status,observed_at,error_code,
		cleanup_requested_at,cloudflare_zone_id,ownership_marker,provider_record_id,
		generation,retry_count,next_attempt_at,created_at,updated_at
		FROM managed_entry_dns WHERE owner_node_id=$1`, ownerNodeID).Scan(
		&a.snapshot.ID, &a.snapshot.OwnerNodeID, &a.snapshot.NodeID, &a.snapshot.Hostname,
		&desired, &observed, &a.snapshot.DesiredAction, &a.snapshot.DNSStatus,
		&a.snapshot.ObservedAt, &a.snapshot.ErrorCode, &a.snapshot.CleanupRequestedAt,
		&a.snapshot.CloudflareZoneID, &a.snapshot.OwnershipMarker, &a.snapshot.ProviderRecordID,
		&a.snapshot.Generation, &a.snapshot.RetryCount, &a.snapshot.NextAttemptAt,
		&a.snapshot.CreatedAt, &a.snapshot.UpdatedAt)
	if err != nil {
		if releaseErr := a.Release(ctx); releaseErr != nil {
			return nil, errors.Join(fmt.Errorf("read managed entry DNS snapshot: %w", err), releaseErr)
		}
		return nil, fmt.Errorf("read managed entry DNS snapshot: %w", err)
	}
	if desired != nil {
		ip, parseErr := netip.ParseAddr(*desired)
		if parseErr != nil {
			return nil, errors.Join(fmt.Errorf("parse desired managed entry DNS IPv4: %w", parseErr), a.Release(ctx))
		}
		a.snapshot.DesiredIPv4 = &ip
	}
	if observed != nil {
		ip, parseErr := netip.ParseAddr(*observed)
		if parseErr != nil {
			return nil, errors.Join(fmt.Errorf("parse observed managed entry DNS IPv4: %w", parseErr), a.Release(ctx))
		}
		a.snapshot.ObservedIPv4 = &ip
	}
	return a, nil
}

func (a *ManagedEntryDNSAttempt) Snapshot() models.ManagedEntryDNS {
	s := a.snapshot
	if s.NodeID != nil {
		value := *s.NodeID
		s.NodeID = &value
	}
	if s.Hostname != nil {
		value := *s.Hostname
		s.Hostname = &value
	}
	if s.DesiredIPv4 != nil {
		value := *s.DesiredIPv4
		s.DesiredIPv4 = &value
	}
	if s.ObservedIPv4 != nil {
		value := *s.ObservedIPv4
		s.ObservedIPv4 = &value
	}
	if s.DNSStatus != nil {
		value := *s.DNSStatus
		s.DNSStatus = &value
	}
	if s.ObservedAt != nil {
		value := *s.ObservedAt
		s.ObservedAt = &value
	}
	if s.ErrorCode != nil {
		value := *s.ErrorCode
		s.ErrorCode = &value
	}
	if s.CleanupRequestedAt != nil {
		value := *s.CleanupRequestedAt
		s.CleanupRequestedAt = &value
	}
	if s.CloudflareZoneID != nil {
		value := *s.CloudflareZoneID
		s.CloudflareZoneID = &value
	}
	if s.OwnershipMarker != nil {
		value := *s.OwnershipMarker
		s.OwnershipMarker = &value
	}
	if s.ProviderRecordID != nil {
		value := *s.ProviderRecordID
		s.ProviderRecordID = &value
	}
	if s.NextAttemptAt != nil {
		value := *s.NextAttemptAt
		s.NextAttemptAt = &value
	}
	return s
}

// Release unlocks on the pinned session even when provider work's context was cancelled.
func (a *ManagedEntryDNSAttempt) Release(_ context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn == nil {
		return nil
	}
	conn := a.conn
	a.conn = nil
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var unlocked bool
	err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1::uuid::text, 29029))`, a.ownerNodeID).Scan(&unlocked)
	if err != nil || !unlocked {
		closeErr := conn.Hijack().Close(ctx)
		if err != nil {
			return errors.Join(fmt.Errorf("unlock managed entry DNS owner: %w", err), closeErr)
		}
		return errors.Join(errors.New("managed entry DNS owner lock was not held"), closeErr)
	}
	conn.Release()
	return nil
}
