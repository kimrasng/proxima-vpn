package deviceegress_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func echoUDP(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, addr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buffer[:n], addr)
		}
	}()
	return conn.LocalAddr().String()
}

func udpClient(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func udpFrame(port int, payload []byte) []byte {
	return append([]byte{0, 0, 0, 1, 203, 0, 113, 1, byte(port >> 8), byte(port)}, payload...)
}

func udpEcho(t *testing.T, client *net.UDPConn, relay *net.UDPAddr, frame []byte) {
	t.Helper()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.WriteToUDP(frame, relay); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 65535)
	n, source, err := client.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if source.String() != relay.String() || !bytes.Equal(buffer[:n], frame) {
		t.Fatalf("wrong UDP echo from %v: length %d, expected %d", source, n, len(frame))
	}
}

func TestUDPChargesPayloadForAllDeviceSessionsAndDirections(t *testing.T) {
	endpoint := echoUDP(t)
	type key struct {
		uuid      string
		direction devicebandwidth.Direction
	}
	var mu sync.Mutex
	totals := make(map[key]int)
	s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
		if size <= 0 || size > devicebandwidth.MaxChunk {
			t.Errorf("unbounded permit %d", size)
		}
		mu.Lock()
		totals[key{uuid, direction}] += size
		mu.Unlock()
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "udp" || address != "203.0.113.1:8443" {
			t.Errorf("unexpected destination %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	var firstRelay string
	for _, uuid := range []string{"device-a", "device-a", "device-b"} {
		control := authenticate(t, s, uuid, "secret-"+uuid[len(uuid)-1:])
		code, relay := request(t, control, 3)
		if code != 0 || !relay.IP.IsLoopback() {
			t.Fatalf("UDP ASSOCIATE: %d %v", code, relay)
		}
		if firstRelay == "" {
			firstRelay = relay.String()
		} else if firstRelay == relay.String() {
			t.Fatal("associations share a UDP socket")
		}
		// Small datagrams also work on hosts with a low UDP maxdgram sysctl.
		client := udpClient(t)
		payload := bytes.Repeat([]byte("p"), 5000)
		for range 8 {
			udpEcho(t, client, relay, udpFrame(8443, payload))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, direction := range []devicebandwidth.Direction{devicebandwidth.Upload, devicebandwidth.Download} {
		if totals[key{"device-a", direction}] != 80000 || totals[key{"device-b", direction}] != 40000 {
			t.Fatalf("incorrect payload totals: %v", totals)
		}
	}
}

func TestUDPRejectsFragmentsAndPinsFirstSourceTuple(t *testing.T) {
	endpoint := echoUDP(t)
	var permits atomic.Int32
	s := startServer(t, func(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error) {
		permits.Add(1)
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	control := authenticate(t, s, "device-a", "secret-a")
	code, relay := request(t, control, 3)
	if code != 0 {
		t.Fatalf("UDP ASSOCIATE: %d", code)
	}
	client := udpClient(t)
	udpEcho(t, client, relay, udpFrame(8443, []byte("pin")))
	fragment := udpFrame(8443, []byte("fragment"))
	fragment[2] = 1
	_, _ = client.WriteToUDP(fragment, relay)
	intruder := udpClient(t)
	_, _ = intruder.WriteToUDP(udpFrame(8443, []byte("intruder")), relay)
	// A subsequent valid echo from the pinned source proves the fragment was
	// processed and discarded without forwarding or invalidating the association.
	udpEcho(t, client, relay, udpFrame(8443, []byte("valid")))
	_ = intruder.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	var buffer [100]byte
	if _, _, err := intruder.ReadFromUDP(buffer[:]); err == nil {
		t.Fatal("unpinned source received traffic")
	}
	if permits.Load() != 4 {
		t.Fatalf("invalid frames charged budget: %d permits", permits.Load())
	}
}

func TestUDPRejectsMalformedAndProhibitedDestinationsWithoutPermits(t *testing.T) {
	endpoint := echoUDP(t)
	var permits atomic.Int32
	s := startServer(t, func(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error) {
		permits.Add(1)
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "203.0.113.1:8443" {
			t.Errorf("dialed prohibited destination %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	control := authenticate(t, s, "device-a", "secret-a")
	code, relay := request(t, control, 3)
	if code != 0 {
		t.Fatalf("UDP ASSOCIATE: %d", code)
	}
	client := udpClient(t)
	for _, frame := range [][]byte{
		{0}, {0, 0, 0, 1, 127}, {0, 0, 0, 9}, {0, 0, 0, 3, 0},
		{1, 0, 0, 1, 203, 0, 113, 1, 0, 80},
		{0, 0, 0, 1, 127, 0, 0, 1, 0, 80, 1},
		{0, 0, 0, 1, 10, 0, 0, 1, 0, 80, 1},
		{0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 80, 1},
		{0, 0, 0, 3, 9, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0, 80, 1},
	} {
		_, _ = client.WriteToUDP(frame, relay)
	}
	udpEcho(t, client, relay, udpFrame(8443, []byte("valid")))
	if permits.Load() != 2 {
		t.Fatalf("invalid datagrams charged budget: %d permits", permits.Load())
	}
}

// observedConn exposes socket closure through the injected transport boundary.
type observedConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *observedConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestUDPLifetimeClosesRemoteSockets(t *testing.T) {
	for _, lifetime := range []string{"control EOF", "revocation", "shutdown"} {
		t.Run(lifetime, func(t *testing.T) {
			endpoint := echoUDP(t)
			closed := make(chan struct{})
			s := startServer(t, allowAll, func(ctx context.Context, network, address string) (net.Conn, error) {
				c, err := (&net.Dialer{}).DialContext(ctx, network, endpoint)
				if err != nil {
					return nil, err
				}
				return &observedConn{Conn: c, closed: closed}, nil
			})
			control := authenticate(t, s, "device-a", "secret-a")
			code, relay := request(t, control, 3)
			if code != 0 {
				t.Fatalf("UDP ASSOCIATE: %d", code)
			}
			udpEcho(t, udpClient(t), relay, udpFrame(8443, []byte("alive")))
			switch lifetime {
			case "control EOF":
				_ = control.Close()
			case "revocation":
				if err := s.SetCredentials(nil); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("remote UDP socket remains open")
			}
			if lifetime != "control EOF" {
				mustClose(t, control)
			}
		})
	}
}

func TestUDPPermitErrorsFailClosedInBothDirections(t *testing.T) {
	for _, denied := range []devicebandwidth.Direction{devicebandwidth.Upload, devicebandwidth.Download} {
		t.Run(string(denied), func(t *testing.T) {
			endpoint := echoUDP(t)
			s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
				if direction == denied {
					return devicebandwidth.PermitResponse{}, errors.New("budget unavailable")
				}
				return devicebandwidth.PermitResponse{Allowed: true}, nil
			}, func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, endpoint)
			})
			control := authenticate(t, s, "device-a", "secret-a")
			code, relay := request(t, control, 3)
			if code != 0 {
				t.Fatalf("UDP ASSOCIATE: %d", code)
			}
			client := udpClient(t)
			_, _ = client.WriteToUDP(udpFrame(8443, []byte("must not echo")), relay)
			mustClose(t, control)
			_ = client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			var buffer [100]byte
			if _, _, err := client.ReadFromUDP(buffer[:]); err == nil {
				t.Fatal("UDP permit error forwarded traffic")
			}
		})
	}
}

func TestUDPHasAtMost64RemoteDestinations(t *testing.T) {
	endpoint := echoUDP(t)
	var dials atomic.Int32
	s := startServer(t, allowAll, func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	control := authenticate(t, s, "device-a", "secret-a")
	code, relay := request(t, control, 3)
	if code != 0 {
		t.Fatalf("UDP ASSOCIATE: %d", code)
	}
	client := udpClient(t)
	for i := range 64 {
		udpEcho(t, client, relay, udpFrame(8000+i, []byte(strconv.Itoa(i))))
	}
	_, _ = client.WriteToUDP(udpFrame(9000, []byte("over limit")), relay)
	udpEcho(t, client, relay, udpFrame(8000, []byte("existing destination")))
	if dials.Load() != 64 {
		t.Fatalf("destination bound exceeded: %d dials", dials.Load())
	}
}

func TestUDPControlEOFCancelsBlockedPermit(t *testing.T) {
	endpoint := echoUDP(t)
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return devicebandwidth.PermitResponse{}, ctx.Err()
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	control := authenticate(t, s, "device-a", "secret-a")
	code, relay := request(t, control, 3)
	if code != 0 {
		t.Fatalf("UDP ASSOCIATE: %d", code)
	}
	_, _ = udpClient(t).WriteToUDP(udpFrame(8443, []byte("wait")), relay)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("permit callback not entered")
	}
	_ = control.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("UDP control EOF did not cancel permit")
	}
}

func TestUDPInvalidFirstDatagramDoesNotClaimWildcardAssociation(t *testing.T) {
	for _, frame := range [][]byte{
		{0}, {0, 0, 0, 1, 127}, {0, 0, 0, 9}, {0, 0, 0, 3, 0},
		{0, 0, 1, 1, 203, 0, 113, 1, 0, 80, 1},
		{0, 0, 0, 1, 127, 0, 0, 1, 0, 80, 1},
	} {
		t.Run(strconv.Itoa(len(frame))+"/"+strconv.Itoa(int(frame[len(frame)-1])), func(t *testing.T) {
			endpoint := echoUDP(t)
			var permits atomic.Int32
			s := startServer(t, func(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error) {
				permits.Add(1)
				return devicebandwidth.PermitResponse{Allowed: true}, nil
			}, func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, endpoint)
			})
			control := authenticate(t, s, "device-a", "secret-a")
			code, relay := request(t, control, 3)
			if code != 0 {
				t.Fatalf("UDP ASSOCIATE: %d", code)
			}
			intruder := udpClient(t)
			_, _ = intruder.WriteToUDP(frame, relay)
			udpEcho(t, udpClient(t), relay, udpFrame(8443, []byte("valid first sender")))
			if permits.Load() != 2 {
				t.Fatalf("invalid first datagram charged budget: %d", permits.Load())
			}
		})
	}
}

func TestUDPSpecifiedSourceRejectsAttackerBeforePinning(t *testing.T) {
	for _, sourceIP := range []net.IP{net.IPv4(127, 0, 0, 1), net.IPv4zero} {
		t.Run(sourceIP.String(), func(t *testing.T) {
			endpoint := echoUDP(t)
			var permits atomic.Int32
			s := startServer(t, func(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error) {
				permits.Add(1)
				return devicebandwidth.PermitResponse{Allowed: true}, nil
			}, func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, endpoint)
			})
			client := udpClient(t)
			port := client.LocalAddr().(*net.UDPAddr).Port
			address := append([]byte{1}, sourceIP.To4()...)
			address = append(address, byte(port>>8), byte(port))
			control := authenticate(t, s, "device-a", "secret-a")
			code, relay := requestWithAddress(t, control, 3, address)
			if code != 0 {
				t.Fatalf("UDP ASSOCIATE: %d", code)
			}
			intruder := udpClient(t)
			_, _ = intruder.WriteToUDP(udpFrame(8443, []byte("attacker")), relay)
			udpEcho(t, client, relay, udpFrame(8443, []byte("specified source")))
			if permits.Load() != 2 {
				t.Fatalf("attacker charged budget: %d", permits.Load())
			}
			_ = intruder.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			var buffer [100]byte
			if _, _, err := intruder.ReadFromUDP(buffer[:]); err == nil {
				t.Fatal("attacker received traffic")
			}
		})
	}
}

func TestUDPAssociateRejectsSourceIPDifferentFromControlPeer(t *testing.T) {
	s := startServer(t, allowAll, nil)
	for _, ip := range []net.IP{net.IPv4(127, 0, 0, 2), net.IPv4(203, 0, 113, 1)} {
		control := authenticate(t, s, "device-a", "secret-a")
		address := append([]byte{1}, ip.To4()...)
		address = append(address, 0, 0)
		if code, _ := requestWithAddress(t, control, 3, address); code != 2 {
			t.Fatalf("mismatched client IP accepted: %s (code %d)", ip, code)
		}
		mustClose(t, control)
	}
}
