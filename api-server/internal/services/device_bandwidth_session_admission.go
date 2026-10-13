package services

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func (s *DeviceBandwidthService) AdmitReportedTunnelSession(ctx context.Context, nodeID, credential, token string) (bool, error) {
	pool, ok := s.db.(*pgxpool.Pool)
	if !ok {
		return false, fmt.Errorf("session admission database unavailable")
	}
	return AdmitReportedTunnelSession(ctx, pool, s.redis, nodeID, credential, token)
}

func AdmitReportedTunnelSession(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, nodeID, credential, token string) (bool, error) {
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
	if db == nil || rdb == nil {
		return false, fmt.Errorf("session admission unavailable")
	}
	report, err := ReadTunnelReport(ctx, rdb, node.String())
	if err != nil {
		return false, err
	}
	var match *ObservedTunnelSession
	for _, connection := range report.Connections {
		reportedDevice, parseErr := uuid.Parse(connection.UUID)
		if parseErr != nil || reportedDevice.String() != device.String() {
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
	service := NewDeviceBandwidthService(db, rdb)
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	user, _, limit, eligible, err := service.authorizeDevice(ctx, tx, node, device)
	if err != nil {
		return false, err
	}
	if !eligible {
		return false, nil
	}
	allowed, err := ReserveTunnelSession(ctx, rdb, user, *match, limit)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return allowed, nil
}
