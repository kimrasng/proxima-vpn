package bandwidth_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// mappedDial is deliberately stricter than an unrestricted localhost override:
// only the fixture's single documentation-only TCP/UDP target can be redirected.
// Any fallback or unintended destination fails, without contacting the Internet.
func mappedDial(t *testing.T, tcpTarget, udpTarget string, count *atomic.Int64) func(context.Context, string, string) (net.Conn, error) {
	t.Helper()
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort(payloadHost, fmt.Sprint(payloadPort)) {
			return nil, fmt.Errorf("isolated harness denied unexpected destination")
		}
		target := ""
		switch network {
		case "tcp", "tcp4", "tcp6":
			target = tcpTarget
		case "udp", "udp4", "udp6":
			target = udpTarget
		}
		if target == "" {
			return nil, fmt.Errorf("isolated harness denied unsupported network")
		}
		if count != nil {
			count.Add(1)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
}

// rejectPayload allows authentication/CONNECT to have succeeded: an established
// VLESS tunnel still must not obtain end-to-end TLS bytes after a limiter outage.
func rejectPayload(t *testing.T, proxy string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxy)
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = c.Write([]byte{5, 1, 0}); err != nil {
		return
	}
	var method [2]byte
	if _, err = io.ReadFull(c, method[:]); err != nil || method != [2]byte{5, 0} {
		return
	}
	request := []byte{5, 1, 0, 1, 198, 51, 100, 10, 1, 187}
	if _, err = c.Write(request); err != nil {
		return
	}
	var reply [10]byte
	if _, err = io.ReadFull(c, reply[:]); err != nil || reply[1] != 0 {
		return
	}
	secure := tls.Client(c, &tls.Config{InsecureSkipVerify: true, ServerName: "bandwidth.test", MinVersion: tls.VersionTLS13}) // fixture only
	if err = secure.HandshakeContext(ctx); err == nil {
		t.Error("unexpected TLS payload reached target: fail-closed route bypassed")
	}
}
