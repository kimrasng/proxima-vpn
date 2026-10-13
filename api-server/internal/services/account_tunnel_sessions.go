package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ObserveAccountTunnelSessions reads every registered Exit, including stale
// ones. A missing/old report is unknown, never an empty fleet. Entry nodes are
// not session owners; source IPs and credentials are not deduplication keys.
func ObserveAccountTunnelSessions(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, userID string) (TunnelSessionObservation, string, error) {
	if db == nil || rdb == nil {
		return TunnelSessionObservation{}, "", fmt.Errorf("session observation unavailable")
	}
	rows, err := db.Query(ctx, `SELECT xray_uuid FROM devices WHERE user_id=$1`, userID)
	if err != nil {
		return TunnelSessionObservation{}, "", err
	}
	owned := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return TunnelSessionObservation{}, "", err
		}
		owned[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TunnelSessionObservation{}, "", err
	}
	rows, err = db.Query(ctx, `SELECT id::text, COALESCE(last_seen>NOW()-interval '25 seconds',false)
 FROM nodes WHERE role IN ('exit','both') AND status<>'pending' ORDER BY id`)
	if err != nil {
		return TunnelSessionObservation{}, "", err
	}
	type node struct {
		id    string
		fresh bool
	}
	nodes := []node{}
	for rows.Next() {
		var n node
		if err := rows.Scan(&n.id, &n.fresh); err != nil {
			rows.Close()
			return TunnelSessionObservation{}, "", err
		}
		nodes = append(nodes, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TunnelSessionObservation{}, "", err
	}
	if len(nodes) == 0 {
		return TunnelSessionObservation{}, "", fmt.Errorf("no Exit nodes")
	}
	observation := TunnelSessionObservation{Complete: true, Sessions: []ObservedTunnelSession{}}
	ids := []string{}
	for _, n := range nodes {
		if !n.fresh {
			observation.Complete = false
			continue
		}
		report, reportID, err := ReadTunnelReportWithID(ctx, rdb, n.id)
		if err != nil {
			observation.Complete = false
			continue
		}
		ids = append(ids, n.id+":"+reportID)
		for _, connection := range report.Connections {
			if owned[connection.UUID] {
				observation.Sessions = append(observation.Sessions, ObservedTunnelSession{ExitID: n.id, Connection: connection})
			}
		}
	}
	if !observation.Complete {
		return observation, "", nil
	}
	if _, err := CountTunnelSessions(observation); err != nil {
		return TunnelSessionObservation{}, "", err
	}
	sort.Strings(ids)
	data, _ := json.Marshal(ids)
	hash := sha256.Sum256(data)
	return observation, hex.EncodeToString(hash[:]), nil
}
