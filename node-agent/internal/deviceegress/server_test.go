package deviceegress_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func startServer(t *testing.T, permit deviceegress.PermitFunc, dial deviceegress.DialFunc) *deviceegress.Server {
	t.Helper()
	s := deviceegress.NewWithDialer(permit, dial)
	if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret-a"}, {UUID: "device-b", Password: "secret-b"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func authenticate(t *testing.T, s *deviceegress.Server, uuid, password string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte{5, 2, 0, 2})
	var response [2]byte
	if _, err := io.ReadFull(c, response[:]); err != nil || response != [2]byte{5, 2} {
		t.Fatalf("authentication method: %v, %v", response, err)
	}
	request := append([]byte{1, byte(len(uuid))}, []byte(uuid)...)
	request = append(request, byte(len(password)))
	request = append(request, []byte(password)...)
	_, _ = c.Write(request)
	if _, err := io.ReadFull(c, response[:]); err != nil || response != [2]byte{1, 0} {
		t.Fatalf("authentication: %v, %v", response, err)
	}
	return c
}

func request(t *testing.T, c net.Conn, command byte) (byte, *net.UDPAddr) {
	t.Helper()
	// An explicit public destination lets the injected transport reach isolated
	// loopback fixtures without weakening production destination checks.
	address := []byte{1, 203, 0, 113, 1, 0x20, 0xfb}
	if command == 3 {
		address = []byte{1, 0, 0, 0, 0, 0, 0}
	}
	return requestWithAddress(t, c, command, address)
}

func requestWithAddress(t *testing.T, c net.Conn, command byte, address []byte) (byte, *net.UDPAddr) {
	t.Helper()
	_, err := c.Write(append([]byte{5, command, 0}, address...))
	if err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		t.Fatal(err)
	}
	var ip net.IP
	switch header[3] {
	case 1:
		ip = make(net.IP, 4)
	case 4:
		ip = make(net.IP, 16)
	default:
		t.Fatalf("unexpected address type %d", header[3])
	}
	if _, err := io.ReadFull(c, ip); err != nil {
		t.Fatal(err)
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		t.Fatal(err)
	}
	return header[1], &net.UDPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(port[:]))}
}

func echoTCP(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return listener.Addr().String()
}

func TestTCPChargesAllSessionsToTheirAuthenticatedDevice(t *testing.T) {
	endpoint := echoTCP(t)
	type key struct {
		uuid      string
		direction devicebandwidth.Direction
	}
	var mu sync.Mutex
	totals := make(map[key]int)
	permit := func(ctx context.Context, uuid string, direction devicebandwidth.Direction, n int) (devicebandwidth.PermitResponse, error) {
		if n <= 0 || n > devicebandwidth.MaxChunk {
			t.Errorf("unbounded permit chunk %d", n)
		}
		mu.Lock()
		totals[key{uuid, direction}] += n
		mu.Unlock()
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}
	s := startServer(t, permit, func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "203.0.113.1:8443" {
			t.Errorf("unexpected destination %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	})
	payload := bytes.Repeat([]byte("x"), 40000)
	for _, uuid := range []string{"device-a", "device-a", "device-b"} {
		password := "secret-" + uuid[len(uuid)-1:]
		c := authenticate(t, s, uuid, password)
		if code, _ := request(t, c, 1); code != 0 {
			t.Fatalf("CONNECT failed: %d", code)
		}
		if _, err := c.Write(payload); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("echo: %v", err)
		}
		_ = c.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	for _, direction := range []devicebandwidth.Direction{devicebandwidth.Upload, devicebandwidth.Download} {
		if totals[key{"device-a", direction}] != 80000 || totals[key{"device-b", direction}] != 40000 {
			t.Errorf("incorrect %s totals: %v", direction, totals)
		}
	}
}

func allowAll(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error) {
	return devicebandwidth.PermitResponse{Allowed: true}, nil
}

func dialEcho(t *testing.T) deviceegress.DialFunc {
	t.Helper()
	endpoint := echoTCP(t)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, endpoint)
	}
}

func mustClose(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var value [1]byte
	_, err := c.Read(value[:])
	if err == nil {
		t.Fatal("connection unexpectedly forwarded data")
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("connection was not closed")
	}
}

func TestAuthenticationIsRequired(t *testing.T) {
	s := startServer(t, allowAll, nil)
	for _, test := range []struct {
		name     string
		greeting []byte
		want     []byte
	}{
		{"no-auth only", []byte{5, 1, 0}, []byte{5, 255}},
		{"unknown method", []byte{5, 1, 1}, []byte{5, 255}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, err := net.Dial("tcp", s.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_, _ = c.Write(test.greeting)
			response := make([]byte, 2)
			if _, err := io.ReadFull(c, response); err != nil || !bytes.Equal(response, test.want) {
				t.Fatalf("method response %v: %v", response, err)
			}
			mustClose(t, c)
		})
	}
	for _, test := range []struct{ uuid, password string }{
		{"device-a", "wrong"},
		{"missing-device", "secret-a"},
	} {
		c, err := net.Dial("tcp", s.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(time.Second))
		_, _ = c.Write([]byte{5, 1, 2})
		var response [2]byte
		_, _ = io.ReadFull(c, response[:])
		message := append([]byte{1, byte(len(test.uuid))}, []byte(test.uuid)...)
		message = append(message, byte(len(test.password)))
		message = append(message, []byte(test.password)...)
		_, _ = c.Write(message)
		if _, err := io.ReadFull(c, response[:]); err != nil || response != [2]byte{1, 1} {
			t.Fatalf("expected failed authentication: %v %v", response, err)
		}
		mustClose(t, c)
		_ = c.Close()
	}
}

func TestTCPRetriesDeniedPermitsBeforeForwarding(t *testing.T) {
	var mu sync.Mutex
	calls := map[devicebandwidth.Direction]int{}
	s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[direction]++
		return devicebandwidth.PermitResponse{Allowed: calls[direction] > 1, RetryAfterMS: 10}, nil
	}, dialEcho(t))
	c := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, c, 1); code != 0 {
		t.Fatalf("CONNECT: %d", code)
	}
	_, _ = c.Write([]byte("hello"))
	got := make([]byte, 5)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "hello" {
		t.Fatalf("echo: %s %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[devicebandwidth.Upload] != 2 || calls[devicebandwidth.Download] != 2 {
		t.Fatalf("permit calls %v", calls)
	}
}

func TestPermitErrorsFailClosedInBothDirections(t *testing.T) {
	for _, denied := range []devicebandwidth.Direction{devicebandwidth.Upload, devicebandwidth.Download} {
		t.Run(string(denied), func(t *testing.T) {
			s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
				if direction == denied {
					return devicebandwidth.PermitResponse{}, errors.New("budget service unavailable")
				}
				return devicebandwidth.PermitResponse{Allowed: true}, nil
			}, dialEcho(t))
			c := authenticate(t, s, "device-a", "secret-a")
			if code, _ := request(t, c, 1); code != 0 {
				t.Fatalf("CONNECT: %d", code)
			}
			_, _ = c.Write([]byte("must not echo"))
			mustClose(t, c)
		})
	}
}

func TestCredentialReplacementRevokesChangedOrRemovedSessionsOnly(t *testing.T) {
	s := startServer(t, allowAll, dialEcho(t))
	a1 := authenticate(t, s, "device-a", "secret-a")
	a2 := authenticate(t, s, "device-a", "secret-a")
	b := authenticate(t, s, "device-b", "secret-b")
	for _, c := range []net.Conn{a1, a2, b} {
		if code, _ := request(t, c, 1); code != 0 {
			t.Fatalf("CONNECT: %d", code)
		}
	}
	if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "changed"}, {UUID: "device-b", Password: "secret-b"}}); err != nil {
		t.Fatal(err)
	}
	mustClose(t, a1)
	mustClose(t, a2)
	_, _ = b.Write([]byte("ok"))
	var echo [2]byte
	if _, err := io.ReadFull(b, echo[:]); err != nil || string(echo[:]) != "ok" {
		t.Fatalf("unchanged device revoked: %v", err)
	}
	changed := authenticate(t, s, "device-a", "changed")
	if err := s.SetCredentials(nil); err != nil {
		t.Fatal(err)
	}
	mustClose(t, b)
	mustClose(t, changed)
}

func TestInvalidCredentialsDoNotReplaceCurrentSet(t *testing.T) {
	s := startServer(t, allowAll, nil)
	for _, credentials := range [][]devicebandwidth.Credential{
		{{UUID: "", Password: "password"}},
		{{UUID: "device-a", Password: ""}},
		{{UUID: "device-a", Password: "password"}, {UUID: "device-a", Password: "password"}},
		{{UUID: string(bytes.Repeat([]byte("x"), 256)), Password: "password"}},
		{{UUID: "device-a", Password: string(bytes.Repeat([]byte("x"), 256))}},
	} {
		if err := s.SetCredentials(credentials); err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
	c := authenticate(t, s, "device-a", "secret-a")
	_ = c.Close()
}

func TestShutdownCancelsPermitWaits(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return devicebandwidth.PermitResponse{}, ctx.Err()
	}, dialEcho(t))
	c := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, c, 1); code != 0 {
		t.Fatalf("CONNECT: %d", code)
	}
	_, _ = c.Write([]byte("wait"))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("permit callback not entered")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("permit callback not cancelled")
	}
	mustClose(t, c)
}

func TestRetryWaitCancelledOnRevocation(t *testing.T) {
	entered := make(chan struct{}, 1)
	s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		return devicebandwidth.PermitResponse{RetryAfterMS: 2147483647}, nil
	}, dialEcho(t))
	c := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, c, 1); code != 0 {
		t.Fatalf("CONNECT: %d", code)
	}
	_, _ = c.Write([]byte("wait"))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("permit callback not entered")
	}
	if err := s.SetCredentials(nil); err != nil {
		t.Fatal(err)
	}
	mustClose(t, c)
}

func TestDestinationsCheckedBeforeDial(t *testing.T) {
	var calls atomic.Int32
	s := startServer(t, allowAll, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("should not dial")
	})
	addresses := []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "224.0.0.1", "0.0.0.0", "::1", "fc00::1", "fe80::1", "ff02::1", "::", "::ffff:127.0.0.1", "localhost"}
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range interfaces {
		if ipNet, ok := address.(*net.IPNet); ok {
			addresses = append(addresses, ipNet.IP.String())
		}
	}
	for _, address := range addresses {
		t.Run(address, func(t *testing.T) {
			c := authenticate(t, s, "device-a", "secret-a")
			frame := []byte{5, 1, 0}
			ip := net.ParseIP(address)
			if ip == nil {
				frame = append(frame, 3, byte(len(address)))
				frame = append(frame, []byte(address)...)
			} else if ipv4 := ip.To4(); ipv4 != nil {
				frame = append(frame, 1)
				frame = append(frame, ipv4...)
			} else {
				frame = append(frame, 4)
				frame = append(frame, ip...)
			}
			frame = append(frame, 0, 80)
			_, _ = c.Write(frame)
			var response [10]byte
			if _, err := io.ReadFull(c, response[:]); err != nil || response[1] != 2 {
				t.Fatalf("denied destination response %v: %v", response, err)
			}
			mustClose(t, c)
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("dialed prohibited destination %d times", calls.Load())
	}
}

func TestListenerRejectsNonLoopbackBindings(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", ":0", "localhost:0", "203.0.113.1:0"} {
		s := deviceegress.New(allowAll)
		if err := s.Start(context.Background(), address); err == nil {
			_ = s.Close()
			t.Errorf("unsafe binding accepted: %s", address)
		}
	}
}

func TestParentCancellationClosesServer(t *testing.T) {
	s := deviceegress.New(allowAll)
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background(), "127.0.0.1:0"); err == nil {
		t.Fatal("closed server restarted")
	}
}
