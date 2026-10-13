package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

// ObserveClientAddresses gathers one account-wide snapshot from fresh Exit
// reports and fresh Entry attribution. This is observational only; it does
// not start the old UUID eviction machinery, which would ban every client
// sharing the account credential.
func ObserveClientAddresses(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, userID string) (ClientAddressObservation, error) {
	rows, err := db.Query(ctx, `SELECT xray_uuid FROM devices WHERE user_id=$1 AND retired_at IS NULL`, userID)
	if err != nil {
		return ClientAddressObservation{}, err
	}
	owned := map[string]bool{}
	for rows.Next() {
		var credential string
		if err := rows.Scan(&credential); err != nil {
			rows.Close()
			return ClientAddressObservation{}, err
		}
		owned[credential] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ClientAddressObservation{}, err
	}

	// Include disabled/removed-plan route participants conservatively: old
	// tunnels can survive a config change. A fleet observation must not infer
	// absence merely from the current publication projection.
	rows, err = db.Query(ctx, `SELECT id::text, role, host(ip),
		COALESCE(last_seen > NOW()-interval '25 seconds', false)
		FROM nodes WHERE role IN ('relay','exit','both') AND status <> 'pending'`)
	if err != nil {
		return ClientAddressObservation{}, err
	}
	type participant struct {
		id, role, address string
		fresh             bool
	}
	participants := []participant{}
	for rows.Next() {
		var p participant
		if err := rows.Scan(&p.id, &p.role, &p.address, &p.fresh); err != nil {
			rows.Close()
			return ClientAddressObservation{}, err
		}
		participants = append(participants, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ClientAddressObservation{}, err
	}

	entries := map[string]bool{}
	reports := []nodeprov.RelayFlowReport{}
	connections := []nodeprov.TunnelConnection{}
	complete := true
	reportIDs := []string{}
	exits := 0
	for _, p := range participants {
		if p.role == "relay" || p.role == "both" {
			entries[p.address] = true
			if !p.fresh {
				complete = false
				continue
			}
			report, reportID, err := ReadRelayFlowsWithID(ctx, rdb, p.id)
			if err != nil {
				complete = false
			} else {
				reportIDs = append(reportIDs, "entry:"+p.id+":"+reportID)
				reports = append(reports, report)
				for _, flow := range report.Flows {
					entries[flow.PeerIP] = true // multihomed Entry egress address
				}
			}
		}
	}
	for _, p := range participants {
		if p.role != "exit" && p.role != "both" {
			continue
		}
		exits++
		if !p.fresh {
			complete = false
			continue
		}
		report, reportID, err := ReadTunnelReportWithID(ctx, rdb, p.id)
		if err != nil {
			complete = false
			continue
		}
		reportIDs = append(reportIDs, "exit:"+p.id+":"+reportID)
		for _, c := range report.Connections {
			if owned[c.UUID] {
				connections = append(connections, c)
			}
		}
	}
	if exits == 0 {
		return ClientAddressObservation{}, fmt.Errorf("no Exit transport observers")
	}
	observation, err := JoinClientAddresses(connections, entries, reports)
	if err != nil {
		return ClientAddressObservation{}, err
	}
	observation.Complete = observation.Complete && complete
	if observation.Complete {
		observation.SnapshotID = clientAddressSnapshotID(reportIDs)
	}
	sort.Strings(observation.Addresses)
	return PreserveClientAddressStarts(ctx, rdb, userID, observation)
}

// Order-independent identity for the exact set of reports used by collection.
func clientAddressSnapshotID(reportIDs []string) string {
	sort.Strings(reportIDs)
	data, _ := json.Marshal(reportIDs)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
