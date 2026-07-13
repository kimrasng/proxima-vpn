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
