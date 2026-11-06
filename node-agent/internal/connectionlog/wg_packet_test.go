package connectionlog

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
)

func wgTestPacket(proto byte, flags byte) []byte {
	n := 28
	if proto == 6 {
		n = 40
	}
	b := make([]byte, n)
	b[0], b[8], b[9] = 0x45, 64, proto
	binary.BigEndian.PutUint16(b[2:4], uint16(n))
	copy(b[12:16], []byte{10, 88, 0, 2})
	copy(b[16:20], []byte{10, 88, 0, 1})
	binary.BigEndian.PutUint16(b[20:22], 40000)
	binary.BigEndian.PutUint16(b[22:24], 8080)
	if proto == 6 {
		b[32], b[33] = 0x50, flags
	} else {
		binary.BigEndian.PutUint16(b[24:26], 8)
	}
	return b
}

func wgTestIPv6(b []byte, extension byte) []byte {
	n := len(b) - 20
	offset := 40
	if extension != 0xff {
		offset += 8
	}
	out := make([]byte, offset+n)
	out[0], out[6], out[7] = 0x60, b[9], 64
	binary.BigEndian.PutUint16(out[4:6], uint16(len(out)-40))
	src, dst := netip.MustParseAddr("fd88::2").As16(), netip.MustParseAddr("fd88::1").As16()
	copy(out[8:24], src[:])
	copy(out[24:40], dst[:])
	if extension != 0xff {
		out[6], out[40] = extension, b[9]
	}
	copy(out[offset:], b[20:])
	return out
}

func TestWGPacketHeaders(t *testing.T) {
	// Given complete inner packets with IPv4 options and IPv6 extensions.
	options := wgTestPacket(17, 0)
	options = append(options[:20], append(make([]byte, 4), options[20:]...)...)
	options[0] = 0x46
	binary.BigEndian.PutUint16(options[2:4], uint16(len(options)))
	tests := []struct {
		name     string
		data     []byte
		protocol byte
		size     uint64
	}{
		{"ipv4 tcp", wgTestPacket(6, 2), 6, 40},
		{"ipv4 options", options, 17, 32},
		{"ipv6 tcp", wgTestIPv6(wgTestPacket(6, 2), 0xff), 6, 60},
		{"ipv6 hop", wgTestIPv6(wgTestPacket(17, 0), 0), 17, 56},
		{"ipv6 destination", wgTestIPv6(wgTestPacket(17, 0), 60), 17, 56},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			p, err := parseWGPacket(tt.data)
			// Then
			if err != nil {
				t.Fatal(err)
			}
			if p.length != tt.size || p.protocol != tt.protocol || p.sourcePort != 40000 || p.destinationPort != 8080 {
				t.Fatalf("wrong metadata: %+v", p)
			}
		})
	}
}

func TestWGPacketRejectsMalformedAndUnsupported(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte) []byte
		want   error
	}{
		{"truncated", func(b []byte) []byte { return b[:30] }, errWGPacket},
		{"bad ihl", func(b []byte) []byte { b[0] = 0x44; return b }, errWGPacket},
		{"bad tcp offset", func(b []byte) []byte { b[32] = 0xf0; return b }, errWGPacket},
		{"zero port", func(b []byte) []byte { b[20] = 0; b[21] = 0; return b }, errWGUnsupportedPacket},
		{"icmp", func(b []byte) []byte { b[9] = 1; return b }, errWGUnsupportedPacket},
		{"ipv6 esp", func(b []byte) []byte { return wgTestIPv6(b, 50) }, errWGUnsupportedPacket},
		{"ipv6 jumbo", func(b []byte) []byte { b = wgTestIPv6(b, 0xff); b[4] = 0; b[5] = 0; return b }, errWGUnsupportedPacket},
		{"active routing", func(b []byte) []byte { b = wgTestIPv6(b, 43); b[43] = 1; return b }, errWGUnsupportedPacket},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given / When
			_, err := parseWGPacket(tt.mutate(wgTestPacket(6, 2)))
			// Then
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}

func FuzzWGPacketNeverPanics(f *testing.F) {
	f.Add(wgTestPacket(6, 2))
	f.Add(wgTestIPv6(wgTestPacket(17, 0), 60))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := parseWGPacket(b)
		if err == nil && p.length > uint64(len(b)) {
			t.Fatal("length outside captured packet")
		}
	})
}
