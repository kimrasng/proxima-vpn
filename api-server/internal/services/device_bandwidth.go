package services

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"github.com/redis/go-redis/v9"
)

const MaxDeviceBandwidthBytes = 65535

var ErrInvalidBandwidthRequest = errors.New("invalid bandwidth permit request")

// DeviceBandwidthPermit grants only the bytes in this request. A denied request
// never reserves future tokens; callers must retry and obtain a fresh permit.
type DeviceBandwidthPermit = devicebandwidth.PermitResponse

type deviceBandwidthDB interface {
	Begin(context.Context) (pgx.Tx, error)
}

// DeviceBandwidthService authorizes every permit against current DB state, then
// reserves an account-wide UUID slot and consumes a central per-device, per-direction Redis bucket. Neither the node ID
// nor the user's ID belongs in the bucket key: all Exits share the same budget.
type DeviceBandwidthService struct {
	db    deviceBandwidthDB
	redis *redis.Client
}

func NewDeviceBandwidthService(db deviceBandwidthDB, rdb *redis.Client) *DeviceBandwidthService {
	return &DeviceBandwidthService{db: db, redis: rdb}
}

func (s *DeviceBandwidthService) Permit(ctx context.Context, nodeID, deviceUUID, direction string, bytes int) (DeviceBandwidthPermit, error) {
	node, nodeErr := uuid.Parse(nodeID)
	device, deviceErr := uuid.Parse(deviceUUID)
	if nodeErr != nil || deviceErr != nil || (direction != "upload" && direction != "download") || bytes < 1 || bytes > MaxDeviceBandwidthBytes {
		return DeviceBandwidthPermit{}, ErrInvalidBandwidthRequest
	}
	if s.db == nil {
		return DeviceBandwidthPermit{}, errors.New("bandwidth authorization database unavailable")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return DeviceBandwidthPermit{}, fmt.Errorf("begin bandwidth authorization: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	finish := func(permit DeviceBandwidthPermit) (DeviceBandwidthPermit, error) {
		if err := tx.Commit(ctx); err != nil {
			return DeviceBandwidthPermit{}, fmt.Errorf("finish bandwidth authorization: %w", err)
		}
		return permit, nil
	}

	userID, mbps, maxConcurrent, eligible, err := s.authorizeDevice(ctx, tx, node, device)
	if err != nil {
		return DeviceBandwidthPermit{}, err
	}
	if !eligible {
		return finish(DeviceBandwidthPermit{})
	}
	if mbps != nil && *mbps > math.MaxInt64/125000 {
		return DeviceBandwidthPermit{}, errors.New("invalid plan bandwidth rate")
	}
	if maxConcurrent < 0 {
		return DeviceBandwidthPermit{}, errors.New("invalid plan concurrent UUID limit")
	}
	if s.redis == nil {
		return DeviceBandwidthPermit{}, errors.New("bandwidth Redis unavailable")
	}
	if maxConcurrent > 0 {
		admitted, err := ReserveAccountUUIDSlot(ctx, s.redis, userID, device.String(), maxConcurrent)
		if err != nil {
			return DeviceBandwidthPermit{}, err
		}
		if !admitted {
			return finish(DeviceBandwidthPermit{DeniedReason: UUIDSlotDeniedReason})
		}
	}
	if mbps == nil || *mbps <= 0 {
		return finish(DeviceBandwidthPermit{Allowed: true})
	}
	rate := *mbps * 125000 // Mbps (decimal megabits/s) -> bytes/s.
	burst := max(int64(MaxDeviceBandwidthBytes), min(rate/10, int64(262144)))
	result, err := deviceBandwidthScript.Run(ctx, s.redis,
		[]string{"device_bandwidth:" + device.String() + ":" + direction}, rate, burst, bytes,
	).Int64Slice()
	if err != nil {
		return DeviceBandwidthPermit{}, fmt.Errorf("consume bandwidth permit: %w", err)
	}
	if len(result) != 2 || (result[0] != 0 && result[0] != 1) || result[1] < 0 || result[1] > 1000 || (result[0] == 1 && result[1] != 0) {
		return DeviceBandwidthPermit{}, errors.New("invalid bandwidth Redis response")
	}
	return finish(DeviceBandwidthPermit{Allowed: result[0] == 1, RetryAfterMS: int(result[1])})
}

// AdmitDevice checks current entitlement and reserves a UUID slot without
// consuming bandwidth. A capacity denial returns false, nil.
func (s *DeviceBandwidthService) AdmitDevice(ctx context.Context, nodeID, deviceUUID string) (bool, error) {
	node, nodeErr := uuid.Parse(nodeID)
	device, deviceErr := uuid.Parse(deviceUUID)
	if nodeErr != nil || deviceErr != nil {
		return false, ErrInvalidBandwidthRequest
	}
	if s.db == nil {
		return false, errors.New("bandwidth authorization database unavailable")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin bandwidth authorization: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	userID, _, maxConcurrent, eligible, err := s.authorizeDevice(ctx, tx, node, device)
	if err != nil {
		return false, err
	}
	admitted := false
	if eligible {
		if maxConcurrent < 0 {
			return false, errors.New("invalid plan concurrent UUID limit")
		}
		if s.redis == nil {
			return false, errors.New("bandwidth Redis unavailable")
		}
		admitted = true
		if maxConcurrent > 0 {
			admitted, err = ReserveAccountUUIDSlot(ctx, s.redis, userID, device.String(), maxConcurrent)
			if err != nil {
				return false, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("finish bandwidth authorization: %w", err)
	}
	return admitted, nil
}

// authorizeDevice holds device, assignment, and plan locks through reservation.
func (s *DeviceBandwidthService) authorizeDevice(ctx context.Context, tx pgx.Tx, node, device uuid.UUID) (string, *int64, int, bool, error) {
	// Lock the assignment before selecting a plan, then lock that exact plan.
	// Separate READ COMMITTED statements see the current row after any lock
	// wait; a joined lock query could otherwise retain a pre-wait plan join.
	// Hold these locks through Redis so older rates cannot outlive a committed
	// downgrade, transfer, device deletion, or ownership change.
	var userID string
	err := tx.QueryRow(ctx, `SELECT user_id::text FROM devices WHERE xray_uuid=$1 FOR SHARE`, device.String()).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, 0, false, nil
	}
	if err != nil {
		return "", nil, 0, false, fmt.Errorf("lock bandwidth device: %w", err)
	}
	var planID *string
	err = tx.QueryRow(ctx, `SELECT plan_id::text FROM users WHERE id=$1 FOR SHARE`, userID).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && planID == nil) {
		return "", nil, 0, false, nil
	}
	if err != nil {
		return "", nil, 0, false, fmt.Errorf("lock bandwidth assignment: %w", err)
	}
	var lockedPlanID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM plans WHERE id=$1 FOR SHARE`, *planID).Scan(&lockedPlanID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, 0, false, nil
	}
	if err != nil {
		return "", nil, 0, false, fmt.Errorf("lock bandwidth plan: %w", err)
	}

	// Re-evaluate eligibility in a fresh statement after all lock waits, and
	// bind the join to the locked user assignment and plan rather than an old
	// snapshot of that assignment.
	var mbps *int64
	var maxConcurrent int
	err = tx.QueryRow(ctx,
		`SELECT p.speed_limit, COALESCE(p.max_concurrent, p.max_devices)
		 FROM devices d
		 JOIN users u ON u.id = d.user_id
		 JOIN plans p ON p.id = u.plan_id
		 JOIN node_group_nodes ngn ON ngn.node_group_id = p.node_group_id
		 JOIN nodes n ON n.id = ngn.node_id
		 WHERE n.id = $1 AND d.xray_uuid = $2 AND u.id = $3 AND p.id = $4
		   AND n.role IN ('exit', 'both') AND n.status != 'pending'
		   AND p.is_active = true
		   AND u.is_active = true AND u.status = 'active'
		   AND (u.plan_expires_at IS NULL OR u.plan_expires_at > NOW())
		   AND (p.traffic_limit IS NULL OR u.traffic_used < p.traffic_limit)
		   AND d.retired_at IS NULL
		   AND (d.evicted_until IS NULL OR d.evicted_until <= NOW())`,
		node.String(), device.String(), userID, lockedPlanID,
	).Scan(&mbps, &maxConcurrent)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, 0, false, nil
	}
	if err != nil {
		return "", nil, 0, false, fmt.Errorf("authorize bandwidth permit: %w", err)
	}
	return userID, mbps, maxConcurrent, true, nil
}

// Redis TIME avoids skew between API servers. The hash stores fractional byte
// tokens and milliseconds, so denial persists refill without borrowing tokens.
// On downgrade, discard old credit and start refilling at the new rate from now;
// never mint a fresh burst or refill the elapsed interval at the old higher rate.
var deviceBandwidthScript = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local requested = tonumber(ARGV[3])
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + tonumber(clock[2]) / 1000
local state = redis.call('HMGET', KEYS[1], 'tokens', 'timestamp', 'rate')
local tokens = burst
local timestamp = now
local previous_rate = rate
if state[1] or state[2] or state[3] then
  tokens = tonumber(state[1])
  timestamp = tonumber(state[2])
  previous_rate = tonumber(state[3])
  if not tokens or not timestamp or not previous_rate or tokens < 0 or previous_rate <= 0 or timestamp > now then
    return redis.error_reply('invalid bandwidth bucket state')
  end
  if previous_rate > rate then
    tokens = 0
  else
    tokens = math.min(burst, tokens + math.max(0, now - timestamp) * previous_rate / 1000)
  end
end
local allowed = 0
local retry_after_ms = 0
if tokens >= requested then
  tokens = tokens - requested
  allowed = 1
else
  retry_after_ms = math.min(1000, math.max(1, math.ceil((requested - tokens) * 1000 / rate)))
end
redis.call('HSET', KEYS[1], 'tokens', tokens, 'timestamp', now, 'rate', rate)
redis.call('EXPIRE', KEYS[1], math.max(60, math.ceil(burst / rate) + 1))
return {allowed, retry_after_ms}
`)
