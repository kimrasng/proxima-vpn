package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
	"github.com/proximavpn/proxima-vpn/pkg/models"
)

type ManagedEntryDNSReconcileDependencies struct {
	Store  *ManagedEntryDNSWorkerStore
	Client *CloudflareDNSClient
	Intent ManagedEntryDNSIntentConfig
	Now    func() time.Time
	Jitter func() float64
}

type ManagedEntryDNSReconciler struct {
	deps ManagedEntryDNSReconcileDependencies
}

func NewManagedEntryDNSReconciler(deps ManagedEntryDNSReconcileDependencies) *ManagedEntryDNSReconciler {
	return &ManagedEntryDNSReconciler{deps: deps}
}

func (r *ManagedEntryDNSReconciler) ReconcileDue(ctx context.Context, limit int) error {
	due, err := r.deps.Store.ListDue(ctx, limit)
	if err != nil {
		return err
	}
	for _, item := range due {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.ReconcileOwner(ctx, item.OwnerNodeID); err != nil && !errors.Is(err, ErrManagedEntryDNSStale) {
			return err
		}
	}
	return nil
}

func (r *ManagedEntryDNSReconciler) ReconcileOwner(ctx context.Context, ownerID string) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	attempt, err := r.deps.Store.TryAcquire(ctx, ownerID)
	if err != nil || attempt == nil {
		return err
	}
	defer func() { result = errors.Join(result, attempt.Release(ctx)) }()
	s := attempt.Snapshot()
	if s.CloudflareZoneID == nil || *s.CloudflareZoneID == "" || s.Hostname == nil || s.OwnershipMarker == nil || *s.OwnershipMarker == "" ||
		!r.deps.Intent.Enabled || r.deps.Intent.base() == "" || *s.CloudflareZoneID != r.deps.Intent.ZoneID ||
		!strings.HasSuffix(*s.Hostname, "."+r.deps.Intent.base()) ||
		(s.DesiredAction == models.ManagedDNSPresent && (s.DesiredIPv4 == nil || !s.DesiredIPv4.Is4() || s.DesiredIPv4.IsUnspecified())) {
		return attempt.Failed(ctx, models.ManagedDNSError, models.ManagedDNSInvalidConfiguration, r.deps.Now(), nil)
	}
	name, err := reality.NormalizeHostname(*s.Hostname)
	if err != nil || name.String() != *s.Hostname {
		return attempt.Failed(ctx, models.ManagedDNSError, models.ManagedDNSInvalidConfiguration, r.deps.Now(), nil)
	}
	if s.DesiredAction != models.ManagedDNSPresent && s.DesiredAction != models.ManagedDNSDelete {
		return attempt.Failed(ctx, models.ManagedDNSError, models.ManagedDNSInvalidConfiguration, r.deps.Now(), nil)
	}
	records, err := r.deps.Client.ListExactAllTypes(ctx, *s.CloudflareZoneID, *s.Hostname)
	if err != nil {
		return r.providerFailure(ctx, attempt, err)
	}
	if s.DesiredAction == models.ManagedDNSDelete {
		return r.reconcileDelete(ctx, attempt, records)
	}
	return r.reconcilePresent(ctx, attempt, records)
}

func (r *ManagedEntryDNSReconciler) providerFailure(ctx context.Context, a *ManagedEntryDNSAttempt, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var provider *CloudflareDNSError
	if !errors.As(err, &provider) {
		return fmt.Errorf("reconcile managed entry DNS: %w", err)
	}
	if provider.Kind == CloudflareDNSTransient {
		var delay time.Duration
		if provider.RetryAfter != nil {
			delay = *provider.RetryAfter
		}
		return a.Transient(ctx, models.ManagedDNSProviderUnavailable, r.deps.Now(), delay, r.deps.Jitter())
	}
	return a.Failed(ctx, models.ManagedDNSError, models.ManagedDNSProviderRejected, r.deps.Now(), nil)
}

func (r *ManagedEntryDNSReconciler) discoverAfterWrite(ctx context.Context, a *ManagedEntryDNSAttempt) ([]CloudflareDNSRecord, error) {
	s := a.Snapshot()
	records, err := r.deps.Client.ListExactAllTypes(ctx, *s.CloudflareZoneID, *s.Hostname)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return records, nil
}
