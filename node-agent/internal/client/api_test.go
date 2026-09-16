package client

import (
	"encoding/json"
	"testing"
)

// Regression: the agent posted {"traffic":[{"uuid","upload","download"}]} while
// /nodes/:id/stats parses {"stats":[{"xray_uuid","up_bytes","dn_bytes"}]}. All
// four names disagreed, so the server decoded an empty stat list, no
// traffic_logs row was ever written and users.traffic_used never moved - which
// also meant traffic limits could never trigger, since enforcement reads that
// column.
//
// The two modules cannot import each other, so this pins the wire names against
// literals copied from handlers.statEntry / handlers.statsRequest. Changing
// either side without the other breaks this test.
func TestStatsPayloadWireNamesMatchTheServerContract(t *testing.T) {
	body, err := json.Marshal(StatsPayload{
		Traffic: []TrafficStat{{
			UUID:     "3f2b7c1e-0a4d-4c8b-9e77-5d1a2b3c4d5e",
			Upload:   1500,
			Download: 3000,
		}},
		OnlineUUIDs: []string{"3f2b7c1e-0a4d-4c8b-9e77-5d1a2b3c4d5e"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Decoded through the server's field names, not the agent's.
	var decoded struct {
		Stats []struct {
			XrayUUID string `json:"xray_uuid"`
			UpBytes  int64  `json:"up_bytes"`
			DnBytes  int64  `json:"dn_bytes"`
		} `json:"stats"`
		OnlineUUIDs []string `json:"online_uuids"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("server-side decode: %v", err)
	}

	if len(decoded.Stats) != 1 {
		t.Fatalf("server would see %d stats, want 1; payload was %s", len(decoded.Stats), body)
	}
	got := decoded.Stats[0]
	if got.XrayUUID != "3f2b7c1e-0a4d-4c8b-9e77-5d1a2b3c4d5e" {
		t.Errorf("xray_uuid = %q, want the device uuid; payload was %s", got.XrayUUID, body)
	}
	if got.UpBytes != 1500 {
		t.Errorf("up_bytes = %d, want 1500", got.UpBytes)
	}
	if got.DnBytes != 3000 {
		t.Errorf("dn_bytes = %d, want 3000", got.DnBytes)
	}
	if len(decoded.OnlineUUIDs) != 1 {
		t.Errorf("online_uuids did not survive the round trip: %s", body)
	}
}

// The heartbeat's observability fields are read by the panel to tell "agent
// alive" from "Xray actually running", so their wire names matter too.
func TestHeartbeatWireNamesMatchTheServerContract(t *testing.T) {
	body, err := json.Marshal(HeartbeatPayload{
		CPU:         12.5,
		XrayVersion: "v1.8.24",
		ConfigHash:  "abc123",
		XrayRunning: true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		CPU         float64 `json:"cpu_usage"`
		XrayVersion string  `json:"xray_version"`
		ConfigHash  string  `json:"config_hash"`
		XrayRunning bool    `json:"xray_running"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("server-side decode: %v", err)
	}

	if decoded.CPU != 12.5 {
		t.Errorf("cpu_usage = %v, want 12.5; payload was %s", decoded.CPU, body)
	}
	if decoded.XrayVersion != "v1.8.24" {
		t.Errorf("xray_version = %q; payload was %s", decoded.XrayVersion, body)
	}
	if decoded.ConfigHash != "abc123" {
		t.Errorf("config_hash = %q; payload was %s", decoded.ConfigHash, body)
	}
	if !decoded.XrayRunning {
		t.Errorf("xray_running did not survive the round trip: %s", body)
	}
}
