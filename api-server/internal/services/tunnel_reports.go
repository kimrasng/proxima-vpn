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

const TunnelReportFreshness = 25 * time.Second

type storedTunnelReport struct {
	nodeprov.TunnelReport
	ReportID string `json:"ReportID,omitempty"`
}

// PublishTunnelReport replaces an authenticated Exit's transport snapshot.
// Unknown/invalid snapshots invalidate prior evidence instead of keeping an
// apparently complete but obsolete list. Server receipt TTL bounds freshness.
func PublishTunnelReport(ctx context.Context, rdb *redis.Client, nodeID string, report nodeprov.TunnelReport) error {
	if rdb == nil {
		return fmt.Errorf("tunnel observation storage unavailable")
	}
	key := "node:" + nodeID + ":tunnels"
	if !report.Known || len(report.Connections) > nodeprov.MaxRelayFlowReport {
		return rdb.Del(ctx, key).Err()
	}
	for _, connection := range report.Connections {
		if err := connection.Validate(); err != nil {
			if clearErr := rdb.Del(ctx, key).Err(); clearErr != nil {
				return clearErr
			}
			return err
		}
	}
	if report.Connections == nil {
		report.Connections = []nodeprov.TunnelConnection{}
	}
	data, err := json.Marshal(storedTunnelReport{TunnelReport: report, ReportID: uuid.NewString()})
	if err != nil {
		return err
	}
	return rdb.Set(ctx, key, data, TunnelReportFreshness).Err()
}

func ReadTunnelReport(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.TunnelReport, error) {
	report, _, err := ReadTunnelReportWithID(ctx, rdb, nodeID)
	return report, err
}

// ReadTunnelReportWithID returns the server receipt identity together with its snapshot.
// Legacy snapshots without an identity are unknown, not empty.
func ReadTunnelReportWithID(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.TunnelReport, string, error) {
	if rdb == nil {
		return nodeprov.TunnelReport{}, "", fmt.Errorf("tunnel observation storage unavailable")
	}
	data, err := rdb.Get(ctx, "node:"+nodeID+":tunnels").Bytes()
	if err != nil {
		return nodeprov.TunnelReport{}, "", err
	}
	var stored storedTunnelReport
	if err := json.Unmarshal(data, &stored); err != nil {
		return nodeprov.TunnelReport{}, "", err
	}
	report := stored.TunnelReport
	if stored.ReportID == "" || !report.Known || report.Connections == nil || len(report.Connections) > nodeprov.MaxRelayFlowReport {
		return nodeprov.TunnelReport{}, "", fmt.Errorf("incomplete tunnel observation")
	}
	for _, connection := range report.Connections {
		if err := connection.Validate(); err != nil {
			return nodeprov.TunnelReport{}, "", err
		}
	}
	return report, stored.ReportID, nil
}
