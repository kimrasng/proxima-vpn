package deviceegress_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
)

func authDenied(t *testing.T, s *deviceegress.Server, uuid, password string) {
	t.Helper()
	c, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	if _, err := c.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	var response [2]byte
	if _, err := io.ReadFull(c, response[:]); err != nil || response != [2]byte{5, 2} {
		t.Fatalf("method: %v %v", response, err)
	}
	request := append([]byte{1, byte(len(uuid))}, []byte(uuid)...)
	request = append(request, byte(len(password)))
	request = append(request, password...)
	if _, err := c.Write(request); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, response[:]); err != nil || response != [2]byte{1, 1} {
		t.Fatalf("expected denial: %v %v", response, err)
	}
}

func TestRevocationClosesOnlyTargetTCPAndCanRecover(t *testing.T) {
	s := startServer(t, allowAll, dialEcho(t))
	first := authenticate(t, s, "device-a", "secret-a")
	second := authenticate(t, s, "device-b", "secret-b")
	for _, c := range []net.Conn{first, second} {
		if code, _ := request(t, c, 1); code != 0 {
			t.Fatalf("connect: %d", code)
		}
	}
	s.ReconcileRevokedUUIDs([]string{"device-a"})
	mustClose(t, first)
	authDenied(t, s, "device-a", "secret-a")
	if _, err := second.Write([]byte("alive")); err != nil {
		t.Fatal(err)
	}
	var echo [5]byte
	if _, err := io.ReadFull(second, echo[:]); err != nil || string(echo[:]) != "alive" {
		t.Fatalf("other device interrupted: %q %v", echo, err)
	}
	s.ReconcileRevokedUUIDs([]string{})
	third := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, third, 1); code != 0 {
		t.Fatalf("recovered connect: %d", code)
	}
}

func TestRevocationClosesUDPAssociationAndPreservesOther(t *testing.T) {
	endpoint := echoUDP(t)
	s := startServer(t, allowAll, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	first := authenticate(t, s, "device-a", "secret-a")
	second := authenticate(t, s, "device-b", "secret-b")
	codeA, relayA := request(t, first, 3)
	codeB, relayB := request(t, second, 3)
	if codeA != 0 || codeB != 0 {
		t.Fatalf("associate: %d %d", codeA, codeB)
	}
	udpA, udpB := udpClient(t), udpClient(t)
	udpEcho(t, udpA, relayA, udpFrame(8443, []byte("a")))
	udpEcho(t, udpB, relayB, udpFrame(8443, []byte("b")))
	s.ReconcileRevokedUUIDs([]string{"device-a"})
	mustClose(t, first)
	// UDP control closure tears down the local socket and its remote datagrams.
	_ = udpA.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	_, _ = udpA.WriteToUDP(udpFrame(8443, []byte("denied")), relayA)
	var data [64]byte
	if n, _, err := udpA.ReadFromUDP(data[:]); err == nil {
		t.Fatalf("revoked association returned %d bytes", n)
	}
	udpEcho(t, udpB, relayB, udpFrame(8443, []byte("still alive")))
}

func TestRevocationUnavailableClosesIdleSessions(t *testing.T) {
	s := startServer(t, allowAll, dialEcho(t))
	c := authenticate(t, s, "device-a", "secret-a")
	s.RevocationUnavailable()
	mustClose(t, c)
	authDenied(t, s, "device-b", "secret-b")
	s.ReconcileRevokedUUIDs([]string{})
	_ = authenticate(t, s, "device-b", "secret-b")
}
