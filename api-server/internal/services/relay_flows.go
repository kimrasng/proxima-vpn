package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

const RelayFlowFreshness = 25 * time.Second

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
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, key, data, RelayFlowFreshness).Err()
}

// ReadRelayFlows never conflates expiration or missing reports with emptiness.
func ReadRelayFlows(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.RelayFlowReport, error) {
	if rdb == nil {
		return nodeprov.RelayFlowReport{}, fmt.Errorf("relay flow observation storage unavailable")
	}
	data, err := rdb.Get(ctx, "node:"+nodeID+":relay_flows").Bytes()
	if err != nil {
		return nodeprov.RelayFlowReport{}, err
	}
	var report nodeprov.RelayFlowReport
	if err := json.Unmarshal(data, &report); err != nil {
		return report, err
	}
	if !report.Known || report.Flows == nil || len(report.Flows) > nodeprov.MaxRelayFlowReport {
		return nodeprov.RelayFlowReport{}, fmt.Errorf("incomplete relay flow report")
	}
	for _, flow := range report.Flows {
		if err := flow.Validate(); err != nil {
			return nodeprov.RelayFlowReport{}, err
		}
	}
	return report, nil
}
