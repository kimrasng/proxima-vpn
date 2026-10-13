//go:build linux

package xray

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestTransportDestroyRequest(t *testing.T) {
	for _, tc := range []struct {
		local, remote string
		family        byte
	}{
		{"192.0.2.1:443", "198.51.100.2:54321", unix.AF_INET},
		{"[2001:db8::1]:443", "[2001:db8::2]:54321", unix.AF_INET6},
	} {
		peer := TransportPeer{Local: netip.MustParseAddrPort(tc.local), Remote: netip.MustParseAddrPort(tc.remote)}
		b, err := transportDestroyRequest(peer)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) != 72 || binary.NativeEndian.Uint16(b[4:6]) != 21 || binary.NativeEndian.Uint16(b[6:8]) != unix.NLM_F_REQUEST|unix.NLM_F_ACK || b[16] != tc.family || b[17] != unix.IPPROTO_TCP || binary.NativeEndian.Uint32(b[20:24]) != 1<<1 || binary.BigEndian.Uint16(b[24:26]) != 443 || binary.BigEndian.Uint16(b[26:28]) != 54321 {
			t.Fatalf("incorrect exact request: %x", b)
		}
		if tc.family == unix.AF_INET {
			if netip.AddrFrom4([4]byte(b[28:32])) != peer.Local.Addr() || netip.AddrFrom4([4]byte(b[44:48])) != peer.Remote.Addr() {
				t.Fatal("wrong IPv4 tuple")
			}
		} else if netip.AddrFrom16([16]byte(b[28:44])) != peer.Local.Addr() || netip.AddrFrom16([16]byte(b[44:60])) != peer.Remote.Addr() {
			t.Fatal("wrong IPv6 tuple")
		}
		if binary.NativeEndian.Uint32(b[60:64]) != 0 || binary.NativeEndian.Uint32(b[64:68]) != ^uint32(0) || binary.NativeEndian.Uint32(b[68:72]) != ^uint32(0) {
			t.Fatal("wrong interface/cookies")
		}
	}
	for _, peer := range []TransportPeer{
		{},
		{Local: netip.MustParseAddrPort("192.0.2.1:0"), Remote: netip.MustParseAddrPort("198.51.100.2:80")},
		{Local: netip.MustParseAddrPort("0.0.0.0:443"), Remote: netip.MustParseAddrPort("198.51.100.2:80")},
		{Local: netip.MustParseAddrPort("192.0.2.1:443"), Remote: netip.MustParseAddrPort("[2001:db8::2]:80")},
		{Local: netip.MustParseAddrPort("[fe80::1%eth0]:443"), Remote: netip.MustParseAddrPort("[fe80::2%eth0]:80")},
	} {
		if _, err := transportDestroyRequest(peer); err == nil {
			t.Fatalf("accepted invalid tuple %+v", peer)
		}
	}
}

func TestTransportDestroyAck(t *testing.T) {
	ack := make([]byte, 36)
	binary.NativeEndian.PutUint32(ack, 36)
	binary.NativeEndian.PutUint16(ack[4:6], unix.NLMSG_ERROR)
	binary.NativeEndian.PutUint32(ack[8:12], 1)
	binary.NativeEndian.PutUint32(ack[20:24], 72)
	binary.NativeEndian.PutUint16(ack[24:26], 21)
	binary.NativeEndian.PutUint32(ack[28:32], 1)
	if err := parseTransportDestroyAck(ack); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(ack); n++ {
		if err := parseTransportDestroyAck(ack[:n]); err == nil {
			t.Fatalf("accepted prefix %d", n)
		}
	}
	for _, errno := range []unix.Errno{unix.EPERM, unix.EOPNOTSUPP, unix.ENOENT} {
		b := append([]byte(nil), ack...)
		binary.NativeEndian.PutUint32(b[16:20], uint32(-int32(errno)))
		if err := parseTransportDestroyAck(b); !errors.Is(err, errno) {
			t.Fatalf("expected %v, got %v", errno, err)
		}
	}
	for _, mutate := range []func([]byte){
		func(b []byte) { binary.NativeEndian.PutUint32(b, 100) },
		func(b []byte) { binary.NativeEndian.PutUint32(b[8:12], 2) },
		func(b []byte) { binary.NativeEndian.PutUint16(b[4:6], unix.NLMSG_DONE) },
		func(b []byte) { binary.NativeEndian.PutUint32(b[16:20], 1) },
		func(b []byte) { binary.NativeEndian.PutUint16(b[24:26], 20) },
	} {
		b := append([]byte(nil), ack...)
		mutate(b)
		if err := parseTransportDestroyAck(b); err == nil {
			t.Fatal("accepted malformed ACK")
		}
	}
	if err := parseTransportDestroyAck(append(ack, ack...)); err == nil {
		t.Fatal("accepted trailing message")
	}
}

func TestCloseTransportPeerLive(t *testing.T) {
	if os.Getenv("TRANSPORT_CLOSE_LIVE") != "1" {
		t.Skip("set TRANSPORT_CLOSE_LIVE=1; requires CAP_NET_ADMIN")
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
		t.Fatal("requires a non-loopback IPv4 interface address")
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IP(address.AsSlice())})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	clients := make([]net.Conn, 0, 2)
	servers := make([]*net.TCPConn, 0, 2)
	for i := 0; i < 2; i++ {
		client, err := net.DialTimeout("tcp4", listener.Addr().String(), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		server, err := listener.AcceptTCP()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = server.Close() }()
		clients, servers = append(clients, client), append(servers, server)
	}
	peer := TransportPeer{Local: servers[0].LocalAddr().(*net.TCPAddr).AddrPort(), Remote: servers[0].RemoteAddr().(*net.TCPAddr).AddrPort()}
	if err := CloseTransportPeer(peer); err != nil {
		t.Fatalf("kernel did not acknowledge exact destroy: %v", err)
	}
	if err := clients[0].SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	if _, err := clients[0].Read(buf[:]); err == nil {
		t.Fatal("target connection still readable")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("target did not close: %v", err)
	}
	if err := servers[1].SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := clients[1].SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := clients[1].Write([]byte{'x'}); err != nil {
		t.Fatalf("other connection write: %v", err)
	}
	if _, err := io.ReadFull(servers[1], buf[:]); err != nil || buf[0] != 'x' {
		t.Fatalf("other connection read: %q %v", buf, err)
	}
	if _, err := servers[1].Write([]byte{'y'}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(clients[1], buf[:]); err != nil || buf[0] != 'y' {
		t.Fatalf("other connection reply: %q %v", buf, err)
	}
	peers, err := TransportPeers([]int{int(peer.Local.Port())})
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].Remote != servers[1].RemoteAddr().(*net.TCPAddr).AddrPort() {
		t.Fatalf("unexpected survivors: %v", peers)
	}
}
