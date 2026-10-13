package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type tunnelReportReceipt struct {
	ReportID   string `json:"report_id"`
	ReceivedAt int64  `json:"received_at"`
}

// ReconcileAbsentTunnelSessions preserves the legacy signature but has no
// receipt evidence, so complete observations cannot confirm an absence.
func ReconcileAbsentTunnelSessions(ctx context.Context, rdb *redis.Client, account string, observation TunnelSessionObservation, snapshotID string, observedAt int64, fence map[string]string) ([]string, error) {
	return ReconcileAbsentTunnelSessionsWithReceipts(ctx, rdb, account, observation, snapshotID, observedAt, fence, nil)
}

// ReconcileAbsentTunnelSessionsWithReceipts requires fresh independent complete
// fleet snapshots separated by 30 seconds using server receipt times for every
// Exit. The report set must be stable across account observation, and the
// pre-observation reservation sequence fence is preserved. This primitive is
// not wired to production admission yet.
func ReconcileAbsentTunnelSessionsWithReceipts(ctx context.Context, rdb *redis.Client, account string, observation TunnelSessionObservation, snapshotID string, observedAt int64, fence map[string]string, receipts map[string]tunnelReportReceipt) ([]string, error) {
	if rdb == nil || observedAt <= 0 {
		return nil, fmt.Errorf("invalid session reconciliation")
	}
	user, err := uuid.Parse(account)
	if err != nil {
		return nil, err
	}
	if observation.Complete && fence == nil {
		return nil, fmt.Errorf("missing pre-observation reservation fence")
	}
	keys := []string{}
	if observation.Complete {
		if snapshotID == "" || len(receipts) == 0 {
			return nil, fmt.Errorf("missing fleet snapshot receipt evidence")
		}
		for nodeID, receipt := range receipts {
			if nodeID == "" || receipt.ReportID == "" || receipt.ReceivedAt <= 0 || receipt.ReceivedAt > observedAt || observedAt-receipt.ReceivedAt > TunnelReportFreshness.Milliseconds() {
				return nil, fmt.Errorf("invalid or stale fleet snapshot receipt evidence")
			}
		}
		if receiptSnapshotID(receipts) != snapshotID {
			return nil, fmt.Errorf("fleet snapshot receipt identity mismatch")
		}
		keys, err = observedTunnelSessionKeys(observation)
		if err != nil {
			return nil, err
		}
		for _, session := range observation.Sessions {
			if _, ok := receipts[session.ExitID]; !ok {
				return nil, fmt.Errorf("session Exit missing from receipt set")
			}
		}
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	before, err := json.Marshal(fence)
	if err != nil {
		return nil, err
	}
	receiptData, err := json.Marshal(receipts)
	if err != nil {
		return nil, err
	}
	const script = `
local epoch=tonumber(ARGV[1])
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
if epoch>now or epoch<now-25000 then redis.call('DEL',KEYS[3]);return redis.error_reply('stale session epoch') end
local previous=tonumber(redis.call('GET',KEYS[4]) or '0')
if ARGV[2]~='1' or ARGV[5]=='' then
 redis.call('DEL',KEYS[3])
 if epoch>previous then redis.call('SET',KEYS[4],epoch) end
 return {}
end
if epoch<=previous then return {} end
redis.call('SET',KEYS[4],epoch)
local current={}
for _,session in ipairs(cjson.decode(ARGV[3])) do current[session]=true end
local fence=cjson.decode(ARGV[4])
local released={}
for _,session in ipairs(redis.call('SMEMBERS',KEYS[1])) do
 local sequence=redis.call('HGET',KEYS[2],session)
 if current[session] or not sequence or fence[session]~=sequence then
  redis.call('HDEL',KEYS[3],session)
 else
  local raw=redis.call('HGET',KEYS[3],session)
  local absence=raw and cjson.decode(raw) or nil
  local prior=(absence and absence.receipts) or {}
  local reportReceipts= cjson.decode(ARGV[6])
  local separated=next(prior)~=nil
  for node,receipt in pairs(reportReceipts) do
   local old=prior[node]
   if not old or old.report_id==receipt.report_id or receipt.received_at-tonumber(old.received_at or '0')<30000 then separated=false end
  end
  for node,_ in pairs(prior) do if not reportReceipts[node] then separated=false end end
  if absence and absence.sequence==sequence and absence.snapshot~=ARGV[5] and separated then
   redis.call('SREM',KEYS[1],session)
   redis.call('HDEL',KEYS[3],session)
   table.insert(released,session)
  elseif not absence or absence.sequence~=sequence or not absence.snapshot or absence.snapshot==ARGV[5] or not separated then
   redis.call('HSET',KEYS[3],session,cjson.encode({sequence=sequence,since=epoch,snapshot=ARGV[5],receipts=reportReceipts}))
  end
 end
end
return released`
	complete := "0"
	if observation.Complete {
		complete = "1"
	}
	key := TunnelSlotKey(user.String())
	result, err := redis.NewScript(script).Run(ctx, rdb, []string{key, key + ":sequence", key + ":absence", key + ":epoch"}, strconv.FormatInt(observedAt, 10), complete, data, before, snapshotID, receiptData).StringSlice()
	if result == nil && err == nil {
		result = []string{}
	}
	return result, err
}

func observedTunnelSessionKeys(observation TunnelSessionObservation) ([]string, error) {
	if !observation.Complete {
		return nil, fmt.Errorf("incomplete tunnel session observation")
	}
	keys := make([]string, 0, len(observation.Sessions))
	exact := make(map[string]ObservedTunnelSession, len(observation.Sessions))
	tupleOwner := make(map[string]string, len(observation.Sessions))
	for _, session := range observation.Sessions {
		key, err := TunnelSessionKey(session)
		if err != nil {
			return nil, err
		}
		if _, ok := exact[key]; ok {
			continue
		}
		connection := session.Connection
		tuple := fmt.Sprintf("%s|%s|%d|%s|%d", session.ExitID, netip.MustParseAddr(connection.LocalIP).Unmap(), connection.LocalPort, netip.MustParseAddr(connection.PeerIP).Unmap(), connection.PeerPort)
		if owner, ok := tupleOwner[tuple]; ok && owner != key {
			return nil, fmt.Errorf("ambiguous tunnel transport ownership")
		}
		tupleOwner[tuple] = key
		exact[key] = session
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func receiptSnapshotID(receipts map[string]tunnelReportReceipt) string {
	ids := make([]string, 0, len(receipts))
	for nodeID, receipt := range receipts {
		ids = append(ids, nodeID+":"+receipt.ReportID)
	}
	sort.Strings(ids)
	data, _ := json.Marshal(ids)
	// Keep the encoding identical to account_tunnel_sessions' fleet identity.
	return sha256Hex(data)
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func CaptureTunnelSessionFence(ctx context.Context, rdb *redis.Client, account string) (map[string]string, error) {
	if rdb == nil {
		return nil, fmt.Errorf("session fence storage unavailable")
	}
	user, err := uuid.Parse(account)
	if err != nil {
		return nil, err
	}
	return rdb.HGetAll(ctx, TunnelSlotKey(user.String())+":sequence").Result()
}

func ObserveAndReconcileTunnelSessions(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, account string) (TunnelSessionObservation, []string, error) {
	if db == nil || rdb == nil {
		return TunnelSessionObservation{}, nil, fmt.Errorf("session reconciliation unavailable")
	}
	fence, err := CaptureTunnelSessionFence(ctx, rdb, account)
	if err != nil {
		return TunnelSessionObservation{}, nil, err
	}
	before, beforeErr := captureExitTunnelReceipts(ctx, db, rdb)
	obs, snapshot, observeErr := ObserveAccountTunnelSessions(ctx, db, rdb, account)
	after, afterErr := captureExitTunnelReceipts(ctx, db, rdb)
	clock, clockErr := rdb.Time(ctx).Result()
	if observeErr != nil || beforeErr != nil || afterErr != nil || clockErr != nil || !sameTunnelReceipts(before, after) || receiptSnapshotID(after) != snapshot {
		obs.Complete = false
		if clockErr == nil {
			_, _ = ReconcileAbsentTunnelSessions(ctx, rdb, account, TunnelSessionObservation{Complete: false}, "", clock.UnixMilli(), fence)
		}
		if observeErr != nil {
			return obs, nil, observeErr
		}
		if beforeErr != nil {
			return obs, nil, beforeErr
		}
		if afterErr != nil {
			return obs, nil, afterErr
		}
		if clockErr != nil {
			return obs, nil, clockErr
		}
		return obs, nil, fmt.Errorf("exit report snapshots changed during account observation")
	}
	released, err := ReconcileAbsentTunnelSessionsWithReceipts(ctx, rdb, account, obs, snapshot, clock.UnixMilli(), fence, after)
	return obs, released, err
}

func captureExitTunnelReceipts(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client) (map[string]tunnelReportReceipt, error) {
	rows, err := db.Query(ctx, `SELECT id::text FROM nodes WHERE role IN ('exit','both') AND status<>'pending' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	nodes := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		nodes = append(nodes, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no Exit nodes")
	}
	receipts := make(map[string]tunnelReportReceipt, len(nodes))
	for _, nodeID := range nodes {
		_, reportID, receivedAt, err := ReadTunnelReportWithReceipt(ctx, rdb, nodeID)
		if err != nil {
			return nil, err
		}
		receipts[nodeID] = tunnelReportReceipt{ReportID: reportID, ReceivedAt: receivedAt}
	}
	return receipts, nil
}

func sameTunnelReceipts(a, b map[string]tunnelReportReceipt) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for nodeID, first := range a {
		if second, ok := b[nodeID]; !ok || first != second {
			return false
		}
	}
	return true
}
