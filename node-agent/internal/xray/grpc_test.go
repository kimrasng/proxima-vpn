package xray

import "testing"

func TestStatsCodecRequestMarshal(t *testing.T) {
	c := statsCodec{}
	b, err := c.Marshal(&queryStatsRequest{Pattern: "user>>>", Reset_: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// field1 tag 0x0A, len 7, "user>>>", field2 tag 0x10, value 1
	want := append([]byte{0x0A, 0x07}, []byte("user>>>")...)
	want = append(want, 0x10, 0x01)
	if string(b) != string(want) {
		t.Errorf("unexpected marshal:\n got %v\nwant %v", b, want)
	}
}

func TestStatsCodecResponseRoundTrip(t *testing.T) {
	// Build a QueryStatsResponse wire payload with two Stat entries.
	stat := func(name string, value uint64) []byte {
		var inner []byte
		inner = append(inner, 0x0A, byte(len(name)))
		inner = append(inner, []byte(name)...)
		inner = append(inner, 0x10)
		for value >= 0x80 {
			inner = append(inner, byte(value)|0x80)
			value >>= 7
		}
		inner = append(inner, byte(value))
		out := []byte{0x0A, byte(len(inner))}
		return append(out, inner...)
	}
	var payload []byte
	payload = append(payload, stat("user>>>u1>>>traffic>>>uplink", 1500)...)
	payload = append(payload, stat("user>>>u1>>>traffic>>>downlink", 3000)...)

	var resp queryStatsResponse
	if err := (statsCodec{}).Unmarshal(payload, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Stat) != 2 {
		t.Fatalf("expected 2 stats, got %d", len(resp.Stat))
	}
	if resp.Stat[0].Name != "user>>>u1>>>traffic>>>uplink" || resp.Stat[0].Value != 1500 {
		t.Errorf("stat0 mismatch: %+v", resp.Stat[0])
	}
	if resp.Stat[1].Value != 3000 {
		t.Errorf("stat1 value mismatch: %+v", resp.Stat[1])
	}

	uuid, dir := parseStatName(resp.Stat[0].Name)
	if uuid != "u1" || dir != "uplink" {
		t.Errorf("parseStatName wrong: %q %q", uuid, dir)
	}
}

// Regression: real stat names carry the client email, not a bare uuid. The
// suffix was returned verbatim, so the server's `WHERE xray_uuid = $1` never
// matched and traffic_used stayed 0. The test above uses a bare "u1" and so
// never exercised the real shape.
func TestParseStatNameStripsTheEmailSuffix(t *testing.T) {
	const id = "3f2b7c1e-0a4d-4c8b-9e77-5d1a2b3c4d5e"

	for _, name := range []string{
		"user>>>" + id + "@proxima>>>traffic>>>uplink",
		"user>>>" + id + "@proxima-vmess>>>traffic>>>uplink",
		"user>>>" + id + "@proxima-trojan>>>traffic>>>uplink",
	} {
		got, dir := parseStatName(name)
		if got != id {
			t.Errorf("%s\n got uuid %q\nwant %q", name, got, id)
		}
		if dir != "uplink" {
			t.Errorf("%s: direction = %q, want uplink", name, dir)
		}
	}
}

func TestParseStatNameRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{
		"",
		"user>>>only-three>>>parts",
		"inbound>>>x@proxima>>>traffic>>>uplink",
		"user>>>x@proxima>>>online>>>uplink",
		"user>>>@proxima>>>traffic>>>uplink",
	} {
		if got, _ := parseStatName(name); got != "" {
			t.Errorf("%q should not yield a uuid, got %q", name, got)
		}
	}
}

// A device's counters arrive once per protocol under the same UUID after the
// suffix is stripped, so they have to sum rather than overwrite each other.
func TestGetUserTrafficSumsPerProtocolCounters(t *testing.T) {
	const id = "3f2b7c1e-0a4d-4c8b-9e77-5d1a2b3c4d5e"

	entries := []statEntry{
		{name: "user>>>" + id + "@proxima>>>traffic>>>uplink", value: 100},
		{name: "user>>>" + id + "@proxima>>>traffic>>>downlink", value: 200},
		{name: "user>>>" + id + "@proxima-vmess>>>traffic>>>uplink", value: 10},
		{name: "user>>>" + id + "@proxima-vmess>>>traffic>>>downlink", value: 20},
	}

	// Mirrors GetUserTraffic's aggregation over queryStats' output, which needs
	// a live Xray to obtain.
	trafficMap := map[string]*TrafficStat{}
	for _, s := range entries {
		uuid, direction := parseStatName(s.name)
		if uuid == "" {
			continue
		}
		ts, ok := trafficMap[uuid]
		if !ok {
			ts = &TrafficStat{UUID: uuid}
			trafficMap[uuid] = ts
		}
		switch direction {
		case "uplink":
			ts.Upload += s.value
		case "downlink":
			ts.Download += s.value
		}
	}

	if len(trafficMap) != 1 {
		t.Fatalf("expected one device, got %d: %v", len(trafficMap), trafficMap)
	}
	ts := trafficMap[id]
	if ts == nil {
		t.Fatalf("no entry for %s: %v", id, trafficMap)
	}
	if ts.Upload != 110 {
		t.Errorf("upload = %d, want 110", ts.Upload)
	}
	if ts.Download != 220 {
		t.Errorf("download = %d, want 220", ts.Download)
	}
}
