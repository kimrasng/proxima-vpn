package xray

import "testing"

func TestMergeOnlineIPsKeepsLatestTimestampAcrossProtocols(t *testing.T) {
	out := map[string]map[string]int64{}
	mergeOnlineIPs(out, "uuid", map[string]int64{"203.0.113.1": 200, "203.0.113.2": 150})
	mergeOnlineIPs(out, "uuid", map[string]int64{"203.0.113.1": 100, "203.0.113.3": 300})
	if out["uuid"]["203.0.113.1"] != 200 || len(out["uuid"]) != 3 {
		t.Fatalf("merged=%v", out)
	}
}
