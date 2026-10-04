package deviceegress_test

import (
	"context"
	"io"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func assertActive(t *testing.T, s interface{ ActiveAdmittedUUIDs() []string }, want []string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := s.ActiveAdmittedUUIDs()
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("active admitted UUIDs = %v, want %v", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestActiveAdmittedTCPAssociationLifetime(t *testing.T) {
	s := startServer(t, allowAll, dialEcho(t))
	first := authenticate(t, s, "device-a", "secret-a")
	second := authenticate(t, s, "device-a", "secret-a")
	other := authenticate(t, s, "device-b", "secret-b")
	assertActive(t, s, nil) // Authentication and CONNECT alone are not admission.
	for _, conn := range []net.Conn{first, second, other} {
		if code, _ := request(t, conn, 1); code != 0 {
			t.Fatalf("CONNECT failed: %d", code)
		}
	}
	assertActive(t, s, nil)
	for _, conn := range []net.Conn{first, second, other} {
		if _, err := conn.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		var echoed [1]byte
		if _, err := io.ReadFull(conn, echoed[:]); err != nil {
			t.Fatal(err)
		}
	}
	assertActive(t, s, []string{"device-a", "device-b"})
	first.Close()
	assertActive(t, s, []string{"device-a", "device-b"})
	second.Close()
	assertActive(t, s, []string{"device-b"})
	s.ReconcileRevokedUUIDs([]string{"device-b"})
	assertActive(t, s, nil)
}

func TestActiveAdmittedUDPAssociationLifetime(t *testing.T) {
	endpoint := echoUDP(t)
	s := startServer(t, allowAll, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	first := authenticate(t, s, "device-a", "secret-a")
	second := authenticate(t, s, "device-a", "secret-a")
	code, relayA := request(t, first, 3)
	if code != 0 {
		t.Fatalf("ASSOCIATE failed: %d", code)
	}
	code, relayB := request(t, second, 3)
	if code != 0 {
		t.Fatalf("ASSOCIATE failed: %d", code)
	}
	assertActive(t, s, nil)
	udpEcho(t, udpClient(t), relayA, udpFrame(8443, []byte("first")))
	udpEcho(t, udpClient(t), relayB, udpFrame(8443, []byte("second")))
	assertActive(t, s, []string{"device-a"})
	first.Close()
	assertActive(t, s, []string{"device-a"})
	second.Close()
	assertActive(t, s, nil)
}

func TestActiveAdmittedExcludesDeniedCapacityAttempts(t *testing.T) {
	var allowed atomic.Bool
	attempted := make(chan struct{}, 1)
	s := startServer(t, func(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error) {
		if allowed.Load() {
			return devicebandwidth.PermitResponse{Allowed: true}, nil
		}
		select {
		case attempted <- struct{}{}:
		default:
		}
		return devicebandwidth.PermitResponse{Allowed: false, RetryAfterMS: 10}, nil
	}, dialEcho(t))
	conn := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, conn, 1); code != 0 {
		t.Fatalf("CONNECT failed: %d", code)
	}
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("capacity denial was not attempted")
	}
	assertActive(t, s, nil)
	allowed.Store(true)
	var echo [1]byte
	if _, err := io.ReadFull(conn, echo[:]); err != nil {
		t.Fatal(err)
	}
	assertActive(t, s, []string{"device-a"})
}

func TestActiveAdmittedExcludesDeniedAndPendingPermits(t *testing.T) {
	var attempts atomic.Int32
	entered := make(chan struct{}, 1)
	s := startServer(t, func(ctx context.Context, _ string, _ devicebandwidth.Direction, _ int) (devicebandwidth.PermitResponse, error) {
		attempts.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return devicebandwidth.PermitResponse{}, ctx.Err()
	}, dialEcho(t))
	conn := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, conn, 1); code != 0 {
		t.Fatalf("CONNECT failed: %d", code)
	}
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("permit was not requested")
	}
	assertActive(t, s, nil)
	conn.Close()
	assertActive(t, s, nil)
	if attempts.Load() == 0 {
		t.Fatal("missing permit attempt")
	}
}
