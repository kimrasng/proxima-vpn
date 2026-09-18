package xray

import (
	"encoding/binary"
	"testing"
)

func encodeOnlineIPList(entries map[string]int64) []byte {
	var buf []byte
	for ip, ts := range entries {
		var entry []byte
		entry = appendTag(entry, 1, 2)
		entry = appendBytes(entry, []byte(ip))
		entry = appendTag(entry, 2, 0)
		entry = binary.AppendUvarint(entry, uint64(ts))

		buf = appendTag(buf, 2, 2)
		buf = appendBytes(buf, entry)
	}
	return buf
}

func TestOnlineIPListDecodesEveryMapEntry(t *testing.T) {
	want := map[string]int64{
		"203.0.113.7":  1758000000,
		"198.51.100.4": 1758000060,
		"2001:db8::1":  1758000120,
	}

	var resp onlineIPListResponse
	if err := unmarshalOnlineIPList(encodeOnlineIPList(want), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.IPs) != len(want) {
		t.Fatalf("decoded %d entries, want %d: %v", len(resp.IPs), len(want), resp.IPs)
	}
	for ip, ts := range want {
		if resp.IPs[ip] != ts {
			t.Errorf("ip %s: last seen %d, want %d", ip, resp.IPs[ip], ts)
		}
	}
}

// A response carrying only the name field means the user has no live
// connection, which must decode to an empty map rather than an error - the
// enforcement pass treats a decode failure as "unknown" and skips the user.
func TestOnlineIPListWithNoConnectionsIsEmptyNotAnError(t *testing.T) {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	buf = appendBytes(buf, []byte("user>>>abc@proxima>>>online"))

	var resp onlineIPListResponse
	if err := unmarshalOnlineIPList(buf, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.IPs) != 0 {
		t.Fatalf("expected no IPs, got %v", resp.IPs)
	}
}

func TestOnlineIPListSkipsUnknownFields(t *testing.T) {
	buf := encodeOnlineIPList(map[string]int64{"203.0.113.7": 1758000000})
	buf = appendTag(buf, 9, 0)
	buf = binary.AppendUvarint(buf, 42)

	var resp onlineIPListResponse
	if err := unmarshalOnlineIPList(buf, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IPs["203.0.113.7"] != 1758000000 {
		t.Fatalf("known entry lost when an unknown field followed: %v", resp.IPs)
	}
}
