package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

const TunnelReportFreshness = 25 * time.Second

type storedTunnelReport struct {
	nodeprov.TunnelReport
	ReportID   string `json:"ReportID,omitempty"`
	ReceivedAt int64  `json:"ReceivedAt,omitempty"`
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
	// ReceivedAt must be the Redis server clock, assigned atomically with the
	// write. The payload is therefore NOT decoded and re-encoded in Lua: cjson
	// turns an empty array into an object, which would corrupt an empty (but
	// complete) connection list into one that cannot be read back. The Go side
	// emits a fixed placeholder token and Lua substitutes only that token.
	const placeholder = "__RECEIVED_AT__"
	data, err := json.Marshal(storedTunnelReport{
		TunnelReport: report,
		ReportID:     uuid.NewString(),
		ReceivedAt:   0, // omitted by omitempty; the placeholder is appended below.
	})
	if err != nil {
		return err
	}
	if len(data) < 2 || data[len(data)-1] != '}' {
		return fmt.Errorf("unexpected tunnel report encoding")
	}
	// Insert the field before the closing brace; the object is never empty
	// because TunnelReport always encodes its fields.
	data = append(data[:len(data)-1], []byte(`,"ReceivedAt":`+placeholder+`}`)...)
	const storeScript = `
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local payload=string.gsub(ARGV[1],ARGV[3],string.format('%.0f',now),1)
redis.call('SET',KEYS[1],payload,'PX',ARGV[2])
return now`
	_, err = redis.NewScript(storeScript).Run(ctx, rdb, []string{key}, string(data), strconv.FormatInt(TunnelReportFreshness.Milliseconds(), 10), placeholder).Result()
	return err
}

func ReadTunnelReport(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.TunnelReport, error) {
	report, _, err := ReadTunnelReportWithID(ctx, rdb, nodeID)
	return report, err
}

// ReadTunnelReportWithID returns the server receipt identity together with its snapshot.
// Legacy snapshots without an identity are unknown, not empty.
func ReadTunnelReportWithID(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.TunnelReport, string, error) {
	report, reportID, _, err := ReadTunnelReportWithReceipt(ctx, rdb, nodeID)
	return report, reportID, err
}

// ReadTunnelReportWithReceipt returns the authenticated report snapshot identity
// and the server-assigned time at which that snapshot was received. Reports
// without both values are incomplete evidence, never empty reports.
func ReadTunnelReportWithReceipt(ctx context.Context, rdb *redis.Client, nodeID string) (nodeprov.TunnelReport, string, int64, error) {
	if rdb == nil {
		return nodeprov.TunnelReport{}, "", 0, fmt.Errorf("tunnel observation storage unavailable")
	}
	data, err := rdb.Get(ctx, "node:"+nodeID+":tunnels").Bytes()
	if err != nil {
		return nodeprov.TunnelReport{}, "", 0, err
	}
	var stored storedTunnelReport
	if err := json.Unmarshal(data, &stored); err != nil {
		return nodeprov.TunnelReport{}, "", 0, err
	}
	report := stored.TunnelReport
	if stored.ReportID == "" || stored.ReceivedAt <= 0 || !report.Known || report.Connections == nil || len(report.Connections) > nodeprov.MaxRelayFlowReport {
		return nodeprov.TunnelReport{}, "", 0, fmt.Errorf("incomplete tunnel observation")
	}
	for _, connection := range report.Connections {
		if err := connection.Validate(); err != nil {
			return nodeprov.TunnelReport{}, "", 0, err
		}
	}
	return report, stored.ReportID, stored.ReceivedAt, nil
}
