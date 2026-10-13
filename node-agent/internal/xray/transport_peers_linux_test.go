//go:build linux

package xray

import (
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestTransportPeersLive observes real sockets without requiring socket control
// privileges. Run in a Linux network namespace with a non-loopback IPv4 address.
func TestTransportPeersLive(t *testing.T) {
	if os.Getenv("TRANSPORT_PEERS_LIVE") != "1" {
		t.Skip("set TRANSPORT_PEERS_LIVE=1 to exercise kernel socket diagnostics")
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var address netip.Addr
	for _, candidate := range addresses {
		prefix, err := netip.ParsePrefix(candidate.String())
		if err == nil && prefix.Addr().Is4() && prefix.Addr().IsGlobalUnicast() && !prefix.Addr().IsLoopback() {
			address = prefix.Addr()
			break
		}
	}
	if !address.IsValid() {
		t.Fatal("live test requires a non-loopback IPv4 interface address")
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IP(address.AsSlice())})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTimeout("tcp4", listener.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	local := server.LocalAddr().(*net.TCPAddr).AddrPort()
	remote := server.RemoteAddr().(*net.TCPAddr).AddrPort()
	if remote != client.LocalAddr().(*net.TCPAddr).AddrPort() {
		t.Fatalf("unexpected client/server tuple: %s -> %s", local, remote)
	}
	ports := []int{int(local.Port())}
	peers, err := TransportPeers(ports)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, peer := range peers {
		if peer.Local == local && peer.Remote == remote {
			found = true
			if peer.Idle < 0 {
				t.Fatalf("negative idle: %v", peer.Idle)
			}
		}
	}
	if !found {
		t.Fatalf("established tuple %s -> %s absent from %v", local, remote, peers)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		peers, err := TransportPeers(ports)
		if err != nil {
			t.Fatal(err)
		}
		if len(peers) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("established peers remain after close: %v", peers)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func transportTestDiag(local, remote string, port uint16, info bool) []byte {
	l, r := netip.MustParseAddr(local), netip.MustParseAddr(remote)
	data := make([]byte, transportDiagSize)
	data[0], data[1] = unix.AF_INET6, 1
	binary.BigEndian.PutUint16(data[4:6], port)
	binary.BigEndian.PutUint16(data[6:8], 54321)
	if l.Is4() {
		data[0] = unix.AF_INET
		la, ra := l.As4(), r.As4()
		copy(data[8:12], la[:])
		copy(data[24:28], ra[:])
	} else {
		la, ra := l.As16(), r.As16()
		copy(data[8:24], la[:])
		copy(data[24:40], ra[:])
	}
	if info {
		attr := make([]byte, 4+104)
		binary.NativeEndian.PutUint16(attr[:2], uint16(len(attr)))
		binary.NativeEndian.PutUint16(attr[2:4], transportDiagInfo)
		binary.NativeEndian.PutUint32(attr[4+56:4+60], 123456)
		data = append(data, attr...)
	}
	return data
}

func transportTestMessage(kind uint16, flags uint16, payload []byte) []byte {
	data := make([]byte, transportAlign(16+len(payload)))
	binary.NativeEndian.PutUint32(data[:4], uint32(16+len(payload)))
	binary.NativeEndian.PutUint16(data[4:6], kind)
	binary.NativeEndian.PutUint16(data[6:8], flags)
	binary.NativeEndian.PutUint32(data[8:12], 7)
	copy(data[16:], payload)
	return data
}

func TestTransportDiagFiltering(t *testing.T) {
	for _, tc := range []struct {
		name, local, remote string
		port                uint16
		keep                bool
	}{
		{"ipv4", "192.0.2.1", "198.51.100.2", 443, true},
		{"ipv6", "2001:db8::1", "2001:db8::2", 443, true},
		{"exact_port", "192.0.2.1", "198.51.100.2", 444, false},
		{"remote_loopback4", "192.0.2.1", "127.0.0.2", 443, false},
		{"local_loopback4", "127.0.0.1", "198.51.100.2", 443, false},
		{"remote_loopback6", "2001:db8::1", "::1", 443, false},
		{"mapped_loopback", "::ffff:192.0.2.1", "::ffff:127.0.0.1", 443, false},
		{"mapped_ipv4", "::ffff:192.0.2.1", "::ffff:198.51.100.2", 443, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := transportTestDiag(tc.local, tc.remote, tc.port, true)
			peer, keep, err := parseTransportDiag(data, data[0], map[uint16]bool{443: true})
			if err != nil || keep != tc.keep {
				t.Fatalf("keep=%v err=%v", keep, err)
			}
			if keep && (peer.Local != netip.AddrPortFrom(netip.MustParseAddr(tc.local).Unmap(), tc.port) || peer.Remote != netip.AddrPortFrom(netip.MustParseAddr(tc.remote).Unmap(), 54321) || peer.Idle != 123456*time.Millisecond) {
				t.Fatalf("unexpected observation: %+v", peer)
			}
		})
	}
}

func TestTransportDumpMultipart(t *testing.T) {
	message := transportTestMessage(transportDiagMessage, unix.NLM_F_MULTI, transportTestDiag("192.0.2.1", "198.51.100.2", 443, true))
	ports := map[uint16]bool{443: true}
	peers, done, err := parseTransportDump(message, 7, unix.AF_INET, ports)
	if err != nil || done || len(peers) != 1 {
		t.Fatalf("peers=%v done=%v err=%v", peers, done, err)
	}
	message = append(message, transportTestMessage(unix.NLMSG_DONE, unix.NLM_F_MULTI, make([]byte, 4))...)
	peers, done, err = parseTransportDump(message, 7, unix.AF_INET, ports)
	if err != nil || !done || len(peers) != 1 {
		t.Fatalf("peers=%v done=%v err=%v", peers, done, err)
	}
	ack := transportTestMessage(unix.NLMSG_ERROR, 0, make([]byte, 20))
	if _, done, err := parseTransportDump(ack, 7, unix.AF_INET, ports); err != nil || done {
		t.Fatalf("ack done=%v err=%v", done, err)
	}
}

func TestTransportDumpRejectsMalformed(t *testing.T) {
	valid := transportTestMessage(transportDiagMessage, unix.NLM_F_MULTI, transportTestDiag("192.0.2.1", "198.51.100.2", 443, true))
	mutate := func(fn func([]byte)) []byte { b := append([]byte(nil), valid...); fn(b); return b }
	badInfo := transportTestDiag("192.0.2.1", "198.51.100.2", 443, true)
	binary.NativeEndian.PutUint16(badInfo[72:74], 8)
	errorPayload := make([]byte, 20)
	binary.NativeEndian.PutUint32(errorPayload, ^uint32(0))
	completion := make([]byte, 4)
	binary.NativeEndian.PutUint32(completion, ^uint32(0))
	cases := map[string][]byte{
		"header":           valid[:15],
		"payload":          valid[:len(valid)-1],
		"short_length":     mutate(func(b []byte) { binary.NativeEndian.PutUint32(b, 15) }),
		"huge_length":      mutate(func(b []byte) { binary.NativeEndian.PutUint32(b, ^uint32(0)) }),
		"sequence":         mutate(func(b []byte) { binary.NativeEndian.PutUint32(b[8:12], 8) }),
		"interrupted":      mutate(func(b []byte) { binary.NativeEndian.PutUint16(b[6:8], unix.NLM_F_DUMP_INTR) }),
		"family":           mutate(func(b []byte) { b[16] = unix.AF_INET6 }),
		"state":            mutate(func(b []byte) { b[17] = 10 }),
		"short_diag":       transportTestMessage(transportDiagMessage, 0, make([]byte, 71)),
		"missing_info":     transportTestMessage(transportDiagMessage, 0, transportTestDiag("192.0.2.1", "198.51.100.2", 443, false)),
		"short_info":       transportTestMessage(transportDiagMessage, 0, badInfo),
		"attr_length":      mutate(func(b []byte) { binary.NativeEndian.PutUint16(b[16+72:], 65535) }),
		"attr_zero":        mutate(func(b []byte) { binary.NativeEndian.PutUint16(b[16+72:], 0) }),
		"short_error":      transportTestMessage(unix.NLMSG_ERROR, 0, make([]byte, 4)),
		"kernel_error":     transportTestMessage(unix.NLMSG_ERROR, 0, errorPayload),
		"done_error":       transportTestMessage(unix.NLMSG_DONE, 0, completion),
		"done_truncated":   transportTestMessage(unix.NLMSG_DONE, 0, []byte{1}),
		"overrun":          transportTestMessage(unix.NLMSG_OVERRUN, 0, nil),
		"after_done":       append(transportTestMessage(unix.NLMSG_DONE, 0, nil), valid...),
		"zero_remote_port": mutate(func(b []byte) { b[22], b[23] = 0, 0 }),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseTransportDump(data, 7, unix.AF_INET, map[uint16]bool{443: true}); err == nil {
				t.Fatal("accepted malformed dump")
			}
		})
	}
	// Every truncated prefix of a single diagnostic message must fail.
	for n := 1; n < len(valid); n++ {
		if _, _, err := parseTransportDump(valid[:n], 7, unix.AF_INET, map[uint16]bool{443: true}); err == nil {
			t.Fatalf("accepted prefix %d", n)
		}
	}
}
