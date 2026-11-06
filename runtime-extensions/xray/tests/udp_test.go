package historytests

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func socksUDP(t *testing.T, ingress int) (*net.UDPConn, int) {
	t.Helper()
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ingress), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { control.Close() })
	if err := control.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 2)
	if _, err := io.ReadFull(control, b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, []byte{5, 0}) {
		t.Fatal("SOCKS negotiation failed")
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	b = make([]byte, 10)
	if _, err := io.ReadFull(control, b); err != nil {
		t.Fatal(err)
	}
	if b[1] != 0 || b[3] != 1 {
		t.Fatalf("SOCKS UDP response %x", b)
	}
	relay := int(binary.BigEndian.Uint16(b[8:]))
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return c, relay
}

func TestAuthenticatedProtocolUDPDestinations(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "shadowsocks", "shadowsocks2022", "shadowsocks2022multi"} {
		t.Run(protocol, func(t *testing.T) {
			// Given actual authenticated runtime UDP paths and two real endpoints.
			one, two, ingress, client := echoUDP(t), echoUDP(t), freePort(t), freePort(t)
			socket := socketPath(t)
			startRuntime(t, config(protocol, ingress, 0, false), socket)
			c := connectCollector(t, socket)
			c.apply(t, 1, true, epoch, protocol)
			startRuntime(t, config(protocol, ingress, client, true), "")
			data, relay := socksUDP(t, client)
			// When both destinations receive distinct known logical datagrams.
			for port, size := range map[int]int{one: 11, two: 7} {
				frame := []byte{0, 0, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)}
				frame = append(frame, bytes.Repeat([]byte{byte(size)}, size)...)
				if _, err := data.WriteToUDP(frame, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: relay}); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 65535)
				n, _, err := data.ReadFromUDP(buf)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(buf[:n], frame) {
					t.Fatalf("UDP payload changed %x", buf[:n])
				}
			}
			// Then the disable barrier includes both observed destination counters,
			// without pretending UDP activity is a remote application disconnect.
			events := c.apply(t, 2, false, epoch, protocol)
			found := make(map[int]bool)
			for _, e := range events {
				if e.Kind != "flow_update" || e.Destination == nil || e.Destination.RequestedPort == nil {
					continue
				}
				port := *e.Destination.RequestedPort
				size, ok := map[int]uint64{one: 11, two: 7}[port]
				if !ok {
					t.Fatalf("unexpected destination %d", port)
				}
				if e.Bytes.Upload == nil || e.Bytes.Download == nil || uint64(*e.Bytes.Upload) != size || uint64(*e.Bytes.Download) != size {
					t.Fatalf("destination %d counters %#v", port, e.Bytes)
				}
				if e.Bytes.Final {
					t.Fatal("disable invented UDP finality")
				}
				wantIdentity := "device"
				if protocol == "shadowsocks" || protocol == "shadowsocks2022" {
					wantIdentity = "shared"
				}
				if e.Auth.Kind != wantIdentity || e.Auth.CredentialRef == nil || string(*e.Auth.CredentialRef) != credential {
					t.Fatalf("wrong UDP authenticated owner: %#v", e.Auth)
				}
				found[port] = true
			}
			if len(found) != 2 {
				t.Fatalf("observed %d of 2 UDP destinations", len(found))
			}
		})
	}
}
