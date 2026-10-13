package services

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
)

var ErrUnknownSourceAttribution = errors.New("relay source requires original client attribution")

// AdmitSourceDevice is direct/cohort admission only. sourceAddress must be the
// server-configured cohort observed by a trusted node, never an x-hwid header.
// Relay integration must supply authenticated original attribution in a separate
// path; an Entry's address must never be counted as the client's address here.
func (s *DeviceBandwidthService) AdmitSourceDevice(ctx context.Context, nodeID, deviceUUID, sourceAddress string) (bool, error) {
	node, nodeErr := uuid.Parse(nodeID)
	device, deviceErr := uuid.Parse(deviceUUID)
	source, sourceErr := netip.ParseAddr(sourceAddress)
	if len(sourceAddress) > 45 || nodeErr != nil || deviceErr != nil || sourceErr != nil || source.Zone() != "" || source.IsUnspecified() || source.IsMulticast() {
		return false, ErrInvalidBandwidthRequest
	}
	source = source.Unmap()
	if !source.IsGlobalUnicast() && !source.IsLoopback() && !source.IsLinkLocalUnicast() {
		return false, ErrInvalidBandwidthRequest
	}
	if s.db == nil {
		return false, errors.New("bandwidth authorization database unavailable")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin source authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	userID, _, cap, eligible, err := s.authorizeDevice(ctx, tx, node, device)
	if err != nil {
		return false, err
	}
	admitted := false
	if eligible {
		if cap < 0 {
			return false, errors.New("invalid plan concurrent address limit")
		}
		// Compare host addresses (ignoring inet masks) and mapped IPv4 too. All
		// registered relays/both nodes are excluded, regardless of status or pool.
		mapped := source.String()
		if source.Is4() {
			mapped = "::ffff:" + mapped
		}
		var relay bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM nodes WHERE role IN ('relay', 'both') AND host(ip)::inet IN ($1::inet, $2::inet))`, source.String(), mapped).Scan(&relay); err != nil {
			return false, fmt.Errorf("check source attribution: %w", err)
		}
		if relay {
			return false, ErrUnknownSourceAttribution
		}
		admitted, err = ReserveObservedClientAddress(ctx, s.redis, userID, source.String(), cap)
		if err != nil {
			return false, fmt.Errorf("reserve observed client address: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("finish source authorization: %w", err)
	}
	return admitted, nil
}
