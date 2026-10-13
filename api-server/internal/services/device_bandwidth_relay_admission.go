package services

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdmitProvenRelaySource reserves a slot only after topology-scoped, fresh
// Entry evidence resolves the exact Exit peer tuple to one original address.
// The Exit API key supplies nodeID; callers must never accept an arbitrary
// original client address in place of that proof.
func (s *DeviceBandwidthService) AdmitProvenRelaySource(ctx context.Context, nodeID, credential, peerIP string, peerPort, localPort int) (bool, error) {
	node, nodeErr := uuid.Parse(nodeID)
	device, deviceErr := uuid.Parse(credential)
	if nodeErr != nil || deviceErr != nil || s.db == nil {
		return false, ErrInvalidBandwidthRequest
	}
	pool, ok := s.db.(*pgxpool.Pool)
	if !ok {
		return false, fmt.Errorf("relay admission requires authoritative topology database")
	}
	source, err := ResolveRelayAdmissionSource(ctx, pool, s.redis, node.String(), peerIP, peerPort, localPort)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	userID, _, limit, eligible, err := s.authorizeDevice(ctx, tx, node, device)
	if err != nil {
		return false, err
	}
	if !eligible {
		return false, nil
	}
	if limit < 0 {
		return false, fmt.Errorf("invalid concurrent address limit")
	}
	allowed, err := ReserveObservedClientAddress(ctx, s.redis, userID, source, limit)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return allowed, nil
}
