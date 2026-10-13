package xray

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// TestLiveSourceRoutingAgainstRealXray requires a disposable Xray 26.7.28
// instance in the same network namespace as the test. Set
// PROXIMA_XRAY_SOURCE_API to its API address and PROXIMA_XRAY_SOURCE_SOCKS
// to a no-auth SOCKS inbound tagged "source-live-in". The API inbound must
// be tagged "api" and a blackhole outbound tagged "block" must exist.
// The test replaces the entire routing table; never point it at production.
// On Docker Desktop run this test binary inside the Xray container so Docker
// port forwarding cannot obscure the two distinct loopback source addresses.
func TestLiveSourceRoutingAgainstRealXray(t *testing.T) {
	api := os.Getenv("PROXIMA_XRAY_SOURCE_API")
	if api == "" {
		t.Skip("PROXIMA_XRAY_SOURCE_API not set (requires disposable Xray 26.7.28)")
	}
	ingress := os.Getenv("PROXIMA_XRAY_SOURCE_SOCKS")
	if ingress == "" {
		t.Fatal("PROXIMA_XRAY_SOURCE_SOCKS must name the source-live-in SOCKS inbound")
	}
	client, err := NewStatsClient(api)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := []RoutingRule{
		{OutboundTag: "api", InboundTag: []string{"api"}},
		{OutboundTag: "block", InboundTag: []string{"source-live-in"}},
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.ReplaceRoutingRules(cleanup, base); err != nil {
			t.Errorf("restore baseline rules: %v", err)
		}
		for _, tag := range []string{"source-live-own", "source-live-other", "source-live-existing"} {
			if err := client.RemoveOutbound(cleanup, tag); err != nil {
				t.Errorf("remove %s: %v", tag, err)
			}
		}
	}()
	for _, marker := range []string{"own", "other", "existing"} {
		port := sourceLiveSOCKS(t, marker)
		if err := client.AddSOCKSOutbound(ctx, SOCKSOutbound{
			Tag: "source-live-" + marker, Address: "127.0.0.1", Port: port,
			Username: "live", Password: "test",
		}); err != nil {
			t.Fatalf("add %s outbound: %v", marker, err)
		}
	}
	rules := func(selected string) []RoutingRule {
		return []RoutingRule{
			base[0],
			{OutboundTag: "source-live-" + selected, InboundTag: []string{"source-live-in"}, SourceCIDRs: []string{"127.0.0.2/32"}},
			{OutboundTag: "source-live-existing", InboundTag: []string{"source-live-in"}, SourceCIDRs: []string{"127.0.0.3/32"}},
			base[1],
		}
	}
	if err := client.ReplaceRoutingRules(ctx, rules("other")); err != nil {
		t.Fatal(err)
	}
	other := sourceLiveConnect(t, ingress, "127.0.0.2", "other")
	_ = other.Close()
	existing := sourceLiveConnect(t, ingress, "127.0.0.3", "existing")
	defer func() { _ = existing.Close() }()
	sourceLiveEcho(t, existing, "before")
	if err := client.ReplaceRoutingRules(ctx, rules("own")); err != nil {
		t.Fatal(err)
	}
	own := sourceLiveConnect(t, ingress, "127.0.0.2", "own")
	defer func() { _ = own.Close() }()
	sourceLiveEcho(t, own, "selected-own")
	// A broad or ignored source selector would incorrectly send .3 to own.
	fresh := sourceLiveConnect(t, ingress, "127.0.0.3", "existing")
	defer func() { _ = fresh.Close() }()
	sourceLiveEcho(t, fresh, "still-existing")
	// Rule replacement must also leave the already-established session alive.
	sourceLiveEcho(t, existing, "after")
}

// sourceLiveSOCKS is an authenticated SOCKS5 sink. Its unique greeting proves
// which outbound Xray actually selected, independently of RPC success.
func sourceLiveSOCKS(t *testing.T, marker string) uint32 {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
					return
				}
				var header [2]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != 5 {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				if _, err := conn.Write([]byte{5, 2}); err != nil {
					return
				}
				if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != 1 {
					return
				}
				username := make([]byte, int(header[1]))
				if _, err := io.ReadFull(conn, username); err != nil {
					return
				}
				var size [1]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				password := make([]byte, int(size[0]))
				if _, err := io.ReadFull(conn, password); err != nil || string(username) != "live" || string(password) != "test" {
					return
				}
				if _, err := conn.Write([]byte{1, 0}); err != nil {
					return
				}
				var request [4]byte
				if _, err := io.ReadFull(conn, request[:]); err != nil || request != [4]byte{5, 1, 0, 1} {
					return
				}
				var target [6]byte
				if _, err := io.ReadFull(conn, target[:]); err != nil {
					return
				}
				if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				if _, err := fmt.Fprintln(conn, marker); err != nil {
					return
				}
				// Closing the client normally terminates this echo loop; the
				// client-side assertions check every payload it sends.
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return uint32(listener.Addr().(*net.TCPAddr).Port)
}

func sourceLiveConnect(t *testing.T, ingress, source, marker string) net.Conn {
	t.Helper()
	dialer := net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}
	conn, err := dialer.Dial("tcp4", ingress)
	if err != nil {
		t.Fatalf("connect from %s: %v", source, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
		t.Fatalf("SOCKS greeting from %s: %v, %v", source, greeting, err)
	}
	// The sink does not dial this destination; its marker identifies the route.
	if _, err := conn.Write([]byte{5, 1, 0, 1, 192, 0, 2, 1, 0, 80}); err != nil {
		t.Fatal(err)
	}
	var reply [10]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || reply[0] != 5 || reply[1] != 0 || reply[3] != 1 {
		t.Fatalf("SOCKS connect from %s: %v, %v", source, reply, err)
	}
	got := make([]byte, len(marker)+1)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != marker+"\n" {
		t.Fatalf("source %s selected %q, want %q: %v", source, got, marker+"\n", err)
	}
	return conn
}

func sourceLiveEcho(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != payload {
		t.Fatalf("session echo: got %q, want %q: %v", got, payload, err)
	}
}
