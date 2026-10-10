package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/models"
)

func (a *ManagedEntryDNSAttempt) Ready(ctx context.Context, observed netip.Addr, providerRecordID string, now time.Time) error {
	if !observed.Is4() || observed.IsUnspecified() || providerRecordID == "" {
		return errors.New("ready managed entry DNS requires IPv4 and provider record ID")
	}
	return a.complete(ctx, `UPDATE managed_entry_dns SET observed_ipv4=$4::inet, observed_at=$5,
		dns_status='ready',provider_record_id=$6,error_code=NULL,retry_count=0,
		next_attempt_at=$5::timestamptz + interval '5 minutes',updated_at=$5
		WHERE id=$1 AND generation=$2 AND desired_action=$3`, observed.String(), now, providerRecordID)
}

func (a *ManagedEntryDNSAttempt) Deleted(ctx context.Context, now time.Time) error {
	return a.complete(ctx, `UPDATE managed_entry_dns SET observed_ipv4=NULL,observed_at=$4,
		dns_status='deleted',error_code=NULL,retry_count=0,
		next_attempt_at=$4::timestamptz + interval '5 minutes',updated_at=$4
		WHERE id=$1 AND generation=$2 AND desired_action=$3`, now)
}

// Failed persists only schema-approved durable status and error combinations.
func (a *ManagedEntryDNSAttempt) Failed(ctx context.Context, status models.ManagedDNSStatus, code models.ManagedDNSErrorCode, now time.Time, next *time.Time) error {
	if status != models.ManagedDNSConflict && status != models.ManagedDNSError {
		return fmt.Errorf("invalid managed entry DNS failure status: %s", status)
	}
	if !managedEntryDNSErrorAllowed(code) {
		return fmt.Errorf("invalid managed entry DNS error code: %s", code)
	}
	return a.complete(ctx, `UPDATE managed_entry_dns SET dns_status=$4,error_code=$5,
		next_attempt_at=$6,updated_at=$7 WHERE id=$1 AND generation=$2 AND desired_action=$3`, status, code, next, now)
}

func (a *ManagedEntryDNSAttempt) Transient(ctx context.Context, code models.ManagedDNSErrorCode, now time.Time, retryAfter time.Duration, jitterUnit float64) error {
	if !managedEntryDNSErrorAllowed(code) {
		return fmt.Errorf("invalid managed entry DNS error code: %s", code)
	}
	status := models.ManagedDNSPending
	if a.snapshot.DesiredAction == models.ManagedDNSDelete {
		status = models.ManagedDNSDeleting
	} else if a.snapshot.DNSStatus != nil && *a.snapshot.DNSStatus == models.ManagedDNSReady &&
		a.snapshot.DesiredIPv4 != nil && a.snapshot.ObservedIPv4 != nil && *a.snapshot.DesiredIPv4 == *a.snapshot.ObservedIPv4 {
		status = models.ManagedDNSReady
	}
	if a.snapshot.RetryCount == math.MaxInt32 {
		return errors.New("managed entry DNS retry count exhausted")
	}
	next := now.Add(ManagedEntryDNSBackoff(a.snapshot.RetryCount, retryAfter, jitterUnit))
	return a.complete(ctx, `UPDATE managed_entry_dns SET dns_status=$4,error_code=$5,
		retry_count=retry_count+1,next_attempt_at=$6,updated_at=$7
		WHERE id=$1 AND generation=$2 AND desired_action=$3
		AND desired_ipv4 IS NOT DISTINCT FROM $8::inet AND retry_count=$9`, status, code, next, now, nullableManagedEntryIP(a.snapshot.DesiredIPv4), a.snapshot.RetryCount)
}

func nullableManagedEntryIP(ip *netip.Addr) *string {
	if ip == nil {
		return nil
	}
	s := ip.String()
	return &s
}

func managedEntryDNSErrorAllowed(code models.ManagedDNSErrorCode) bool {
	switch code {
	case models.ManagedDNSProviderUnavailable, models.ManagedDNSProviderRejected,
		models.ManagedDNSOwnershipConflict, models.ManagedDNSInvalidConfiguration, models.ManagedDNSMismatch:
		return true
	default:
		return false
	}
}

func (a *ManagedEntryDNSAttempt) complete(ctx context.Context, query string, args ...any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn == nil {
		return ErrManagedEntryDNSStale
	}
	params := append([]any{a.snapshot.ID, a.snapshot.Generation, a.snapshot.DesiredAction}, args...)
	tag, err := a.conn.Exec(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("complete managed entry DNS attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrManagedEntryDNSStale
	}
	return nil
}
