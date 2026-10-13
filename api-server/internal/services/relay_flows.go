package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

const RelayFlowFreshness = 25 * time.Second

type storedRelayFlowReport struct {
	nodeprov.RelayFlowReport
	ReportID string `json:"ReportID,omitempty"`
}

// PublishRelayFlows stores one authenticated Entry snapshot with a short TTL.
// Unknown and malformed reports invalidate the prior snapshot immediately;
// they must never be interpreted as an empty set of connected clients.
func PublishRelayFlows(ctx context.Context, rdb *redis.Client, nodeID string, report nodeprov.RelayFlowReport) error {
	if rdb == nil {
		return fmt.Errorf("relay flow observation storage unavailable")
	}
	key := "node:" + nodeID + ":relay_flows"
	if !report.Known || len(report.Flows) > nodeprov.MaxRelayFlowReport {
		return rdb.Del(ctx, key).Err()
	}
	for _, flow := range report.Flows {
		if err := flow.Validate(); err != nil {
			if clearErr := rdb.Del(ctx, key).Err(); clearErr != nil {
				return clearErr
			}
			return err
		}
	}
	if report.Flows == nil {
		report.Flows = []nodeprov.RelayFlow{}
	}
	data, err := json.Marshal(storedRelayFlowReport{RelayFlowReport: report, ReportID: uuid.NewString()})
	if err != nil {
		return err
	}
	return rdb.Set(ctx, key, data, RelayFlowFreshness).Err()
}

// ReadRelayFlows never conflates expiration or missing reports with emptiness.
func ReadRelayFlows(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.RelayFlowReport, error) {
	report, _, err := ReadRelayFlowsWithID(ctx, rdb, nodeID)
	return report, err
}

// ReadRelayFlowsWithID returns the server receipt identity together with its snapshot.
// Legacy snapshots without an identity are unknown, not empty.
func ReadRelayFlowsWithID(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.RelayFlowReport, string, error) {
	if rdb == nil {
		return nodeprov.RelayFlowReport{}, "", fmt.Errorf("relay flow observation storage unavailable")
	}
	data, err := rdb.Get(ctx, "node:"+nodeID+":relay_flows").Bytes()
	if err != nil {
		return nodeprov.RelayFlowReport{}, "", err
	}
	var stored storedRelayFlowReport
	if err := json.Unmarshal(data, &stored); err != nil {
		return nodeprov.RelayFlowReport{}, "", err
	}
	report := stored.RelayFlowReport
	if stored.ReportID == "" || !report.Known || report.Flows == nil || len(report.Flows) > nodeprov.MaxRelayFlowReport {
		return nodeprov.RelayFlowReport{}, "", fmt.Errorf("incomplete relay flow report")
	}
	for _, flow := range report.Flows {
		if err := flow.Validate(); err != nil {
			return nodeprov.RelayFlowReport{}, "", err
		}
	}
	return report, stored.ReportID, nil
}
