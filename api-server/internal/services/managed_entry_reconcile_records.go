package services

import (
	"context"
	"sort"
	"strings"

	"github.com/proximavpn/proxima-vpn/pkg/models"
)

type managedEntryRecordSet struct {
	owned   []CloudflareDNSRecord
	foreign bool
	invalid bool
}

func partitionManagedEntryRecords(records []CloudflareDNSRecord, s models.ManagedEntryDNS) managedEntryRecordSet {
	set := managedEntryRecordSet{}
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSuffix(record.Name, "."), *s.Hostname) {
			continue
		}
		if record.Type == "A" && record.Comment == *s.OwnershipMarker && record.ID != "" {
			set.owned = append(set.owned, record)
		} else if record.Type == "A" && record.Comment == *s.OwnershipMarker {
			set.invalid = true
		} else if record.Type == "A" || record.Type == "CNAME" || record.Type == "NS" {
			set.foreign = true
		}
	}
	sort.Slice(set.owned, func(i, j int) bool { return set.owned[i].ID < set.owned[j].ID })
	return set
}

func managedEntryKeeper(set managedEntryRecordSet, cached *string) CloudflareDNSRecord {
	if cached != nil {
		for _, record := range set.owned {
			if record.ID == *cached {
				return record
			}
		}
	}
	return set.owned[0]
}

func managedEntryMatches(record CloudflareDNSRecord, s models.ManagedEntryDNS) bool {
	return record.Content == s.DesiredIPv4.String() && record.TTL == 300 && !record.Proxied && record.Comment == *s.OwnershipMarker
}

func (r *ManagedEntryDNSReconciler) reconcilePresent(ctx context.Context, a *ManagedEntryDNSAttempt, records []CloudflareDNSRecord) error {
	s := a.Snapshot()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		set := partitionManagedEntryRecords(records, s)
		if set.foreign || set.invalid {
			return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
		}
		if len(set.owned) == 0 {
			_, writeErr := r.deps.Client.Create(ctx, *s.CloudflareZoneID, *s.Hostname, s.DesiredIPv4.String(), *s.OwnershipMarker)
			var err error
			records, err = r.discoverAfterWrite(ctx, a)
			if err != nil {
				return r.providerFailure(ctx, a, err)
			}
			set = partitionManagedEntryRecords(records, s)
			if writeErr != nil {
				if !set.foreign && !set.invalid && len(set.owned) == 1 && managedEntryMatches(set.owned[0], s) {
					return a.Ready(ctx, *s.DesiredIPv4, set.owned[0].ID, r.deps.Now())
				}
				return r.providerFailure(ctx, a, writeErr)
			}
			if len(set.owned) == 0 {
				return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
			}
			if set.foreign || set.invalid {
				return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
			}
			continue
		}
		keeper := managedEntryKeeper(set, s.ProviderRecordID)
		if len(set.owned) > 1 {
			var duplicate CloudflareDNSRecord
			for _, record := range set.owned {
				if record.ID != keeper.ID {
					duplicate = record
					break
				}
			}
			writeErr := r.deps.Client.Delete(ctx, *s.CloudflareZoneID, duplicate.ID)
			var err error
			records, err = r.discoverAfterWrite(ctx, a)
			if err != nil {
				return r.providerFailure(ctx, a, err)
			}
			if writeErr != nil {
				remaining := partitionManagedEntryRecords(records, s)
				if remaining.foreign || remaining.invalid {
					return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
				}
				for _, record := range remaining.owned {
					if record.ID == duplicate.ID {
						return r.providerFailure(ctx, a, writeErr)
					}
				}
			}
			for _, record := range partitionManagedEntryRecords(records, s).owned {
				if record.ID == duplicate.ID {
					return a.Transient(ctx, models.ManagedDNSProviderUnavailable, r.deps.Now(), 0, r.deps.Jitter())
				}
			}
			retained := false
			for _, record := range partitionManagedEntryRecords(records, s).owned {
				if record.ID == keeper.ID {
					retained = true
				}
			}
			if !retained {
				return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
			}
			continue
		}
		if !managedEntryMatches(keeper, s) {
			_, writeErr := r.deps.Client.Update(ctx, *s.CloudflareZoneID, keeper.ID, *s.Hostname, s.DesiredIPv4.String(), *s.OwnershipMarker)
			var err error
			records, err = r.discoverAfterWrite(ctx, a)
			if err != nil {
				return r.providerFailure(ctx, a, err)
			}
			if writeErr != nil {
				updated := partitionManagedEntryRecords(records, s)
				if !updated.foreign && !updated.invalid && len(updated.owned) == 1 && updated.owned[0].ID == keeper.ID && managedEntryMatches(updated.owned[0], s) {
					return a.Ready(ctx, *s.DesiredIPv4, keeper.ID, r.deps.Now())
				}
				return r.providerFailure(ctx, a, writeErr)
			}
			updated := partitionManagedEntryRecords(records, s)
			if updated.foreign || updated.invalid {
				return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
			}
			if len(updated.owned) != 1 || updated.owned[0].ID != keeper.ID {
				return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
			}
			if !managedEntryMatches(updated.owned[0], s) {
				return a.Transient(ctx, models.ManagedDNSProviderUnavailable, r.deps.Now(), 0, r.deps.Jitter())
			}
			continue
		}
		return a.Ready(ctx, *s.DesiredIPv4, keeper.ID, r.deps.Now())
	}
}

func (r *ManagedEntryDNSReconciler) reconcileDelete(ctx context.Context, a *ManagedEntryDNSAttempt, records []CloudflareDNSRecord) error {
	s := a.Snapshot()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		set := partitionManagedEntryRecords(records, s)
		if set.invalid {
			return a.Failed(ctx, models.ManagedDNSConflict, models.ManagedDNSOwnershipConflict, r.deps.Now(), nil)
		}
		if len(set.owned) == 0 {
			return a.Deleted(ctx, r.deps.Now())
		}
		id := set.owned[0].ID
		writeErr := r.deps.Client.Delete(ctx, *s.CloudflareZoneID, id)
		var err error
		records, err = r.discoverAfterWrite(ctx, a)
		if err != nil {
			return r.providerFailure(ctx, a, err)
		}
		if writeErr != nil {
			for _, record := range partitionManagedEntryRecords(records, s).owned {
				if record.ID == id {
					return r.providerFailure(ctx, a, writeErr)
				}
			}
		}
		for _, record := range partitionManagedEntryRecords(records, s).owned {
			if record.ID == id {
				return a.Transient(ctx, models.ManagedDNSProviderUnavailable, r.deps.Now(), 0, r.deps.Jitter())
			}
		}
	}
}
