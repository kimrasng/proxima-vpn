package services

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdmitReportedTunnelSession admits only an exact authenticated transport in
// this Exit's fresh report and reserves one account-wide session slot while
// current plan/device entitlement locks are held.
func (s *DeviceBandwidthService) AdmitReportedTunnelSession(ctx context.Context, nodeID, credential, token string) (bool, error) {
	node, err := uuid.Parse(nodeID)
	if err != nil {
		return false, ErrInvalidBandwidthRequest
	}
	device, err := uuid.Parse(credential)
	if err != nil {
		return false, ErrInvalidBandwidthRequest
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != token {
		return false, ErrInvalidBandwidthRequest
	}
	if s.db == nil || s.redis == nil {
		return false, fmt.Errorf("session admission unavailable")
	}
	report, err := ReadTunnelReport(ctx, s.redis, node.String())
	if err != nil {
		return false, err
	}
	var match *ObservedTunnelSession
	for _, connection := range report.Connections {
		if connection.UUID != device.String() {
			continue
		}
		candidate := ObservedTunnelSession{ExitID: node.String(), Connection: connection}
		key, err := TunnelSessionKey(candidate)
		if err != nil {
			return false, err
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(token)) == 1 {
			copy := candidate
			match = &copy
			break
		}
	}
	if match == nil {
		return false, fmt.Errorf("session absent from fresh authenticated Exit report")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	user, _, limit, eligible, err := s.authorizeDevice(ctx, tx, node, device)
	if err != nil {
		return false, err
	}
	if !eligible {
		return false, nil
	}
	allowed, err := ReserveTunnelSession(ctx, s.redis, user, *match, limit)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return allowed, nil
}

// SessionEntitled validates active account/device/plan and Exit membership
// without consuming a session slot. Reserved for deny-first learning; do not
// route the new session to egress until a subsequent exact-token admission.
func SessionEntitled(ctx context.Context, db *pgxpool.Pool, nodeID, credential string) (bool, error) {
	node, nodeErr := uuid.Parse(nodeID)
	device, deviceErr := uuid.Parse(credential)
	if nodeErr != nil || deviceErr != nil {
		return false, ErrInvalidBandwidthRequest
	}
	if db == nil {
		return false, fmt.Errorf("session entitlement database unavailable")
	}
	service := NewDeviceBandwidthService(db, nil)
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, _, _, eligible, err := service.authorizeDevice(ctx, tx, node, device)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return eligible, nil
}
