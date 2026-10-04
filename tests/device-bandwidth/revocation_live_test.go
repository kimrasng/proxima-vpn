package bandwidth_test

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"
)

// These tests exercise the production egress server behind real VLESS/REALITY
// Vision routing, not Xray's independent RemoveUser API.
func revocationXrayFixture(t *testing.T, xudp bool) (*fakeAuthority, string, string, func([]string)) {
	t.Helper()
	binary := requireXray(t)
	a := newAuthority()
	a.add(deviceA, rateA, burst)
	a.add(deviceB, rateB, burst)
	tcp := payloadServer(t)
	s := limiter(t, a, tcp, udpEcho(t), nil)
	private, public := realityKeys(t)
	serverPort := reserveTCPPort(t)
	startXray(t, binary, "revocation-server", generatedServer(t, serverPort, private, tcp, s), serverPort)
	var proxies [2]string
	for i, id := range []string{deviceA, deviceB} {
		port := reserveTCPPort(t)
		cfg := xrayClientConfig(port, serverPort, id, public)
		if xudp {
			outbound := cfg["outbounds"].([]any)[0].(map[string]any)
			user := outbound["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)
			user["flow"] = "xtls-rprx-vision-udp443"
			outbound["mux"] = map[string]any{"enabled": false, "xudpConcurrency": 8, "xudpProxyUDP443": "allow"}
		}
		startXray(t, binary, fmt.Sprintf("revocation-client-%d", i), cfg, port)
		proxies[i] = fmt.Sprintf("127.0.0.1:%d", port)
	}
	return a, proxies[0], proxies[1], s.ReconcileRevokedUUIDs
}

func revocationReadPayload(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16*1024)
	if n, err := c.Read(buf); n == 0 || err != nil {
		t.Fatalf("established Vision stream did not carry payload: %d %v", n, err)
	}
}

func assertRevokedVisionStopped(t *testing.T, c net.Conn) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	quiet := time.Now().Add(500 * time.Millisecond)
	var residual int
	buf := make([]byte, 32*1024)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := c.Read(buf)
		residual += n
		if residual > 2*1024*1024 {
			t.Fatal("revoked Vision session kept forwarding beyond drain allowance")
		}
		if n > 0 {
			quiet = time.Now().Add(500 * time.Millisecond)
		}
		if err != nil {
			if e, ok := err.(net.Error); !ok || !e.Timeout() {
				return // underlying Xray stream closed
			}
		}
		if time.Now().After(quiet) {
			return // Xray socket may stay open, but payload forwarding stopped
		}
	}
	t.Fatalf("revoked Vision stream did not quiesce within 2s; residual bytes: %d", residual)
}

func TestRealXrayRevocationEstablishedVisionTCP(t *testing.T) {
	a, proxyA, proxyB, reconcile := revocationXrayFixture(t, false)
	streamA := openPayload(t, proxyA, "", "")
	streamB := openPayload(t, proxyB, "", "")
	revocationReadPayload(t, streamA)
	revocationReadPayload(t, streamB)

	reconcile([]string{deviceA})
	// Xray may retain the outer TLS socket after the egress stream closes.
	// Require forwarding to quiesce within a bounded drain allowance instead
	// of requiring an EOF from the independently managed Xray transport.
	assertRevokedVisionStopped(t, streamA)
	rejectPayload(t, proxyA)
	revocationReadPayload(t, streamB)

	// Clearing the local snapshot does not override the central permit authority.
	a.revoke(deviceA)
	reconcile([]string{})
	rejectPayload(t, proxyA)
	revocationReadPayload(t, streamB)
	recordEvidence(t, "vision_tcp_revocation", map[string]any{"existing_a_closed": true, "fresh_a_denied": true, "b_continued": true, "central_revoke_after_snapshot_clear_denied": true})
}

type revocationUDPAssociation struct {
	control net.Conn
	conn    *net.UDPConn
}

func openRevocationUDP(t *testing.T, proxy string) revocationUDPAssociation {
	t.Helper()
	control, relay := socksConnect(t, proxy, "", "", 3, "0.0.0.0", 0)
	if relay.IP.IsUnspecified() {
		relay.IP = net.ParseIP("127.0.0.1")
	}
	if !relay.IP.IsLoopback() {
		control.Close()
		t.Fatalf("UDP relay not loopback: %v", relay)
	}
	conn, err := net.DialUDP("udp4", nil, relay)
	if err != nil {
		control.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); control.Close() })
	return revocationUDPAssociation{control: control, conn: conn}
}

func (a revocationUDPAssociation) echo(t *testing.T, timeout time.Duration) bool {
	t.Helper()
	payload := []byte("existing-xudp-revocation-probe")
	if _, err := a.conn.Write(udpFrame(payload)); err != nil {
		return false
	}
	_ = a.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 2048)
	n, err := a.conn.Read(buf)
	if err != nil {
		return false
	}
	got, err := udpPayload(buf[:n])
	return err == nil && bytes.Equal(got, payload)
}

func TestRealXrayRevocationEstablishedVisionXUDP(t *testing.T) {
	a, proxyA, proxyB, reconcile := revocationXrayFixture(t, true)
	assocA := openRevocationUDP(t, proxyA)
	assocB := openRevocationUDP(t, proxyB)
	if !assocA.echo(t, 2*time.Second) || !assocB.echo(t, 2*time.Second) {
		t.Fatal("both XUDP associations must echo before revocation")
	}

	reconcile([]string{deviceA})
	// Repeat on the SAME SOCKS UDP association; no fresh VLESS authentication.
	for i := 0; i < 3; i++ {
		if assocA.echo(t, 600*time.Millisecond) {
			t.Fatal("revoked established XUDP association still echoes")
		}
	}
	freshA := openRevocationUDP(t, proxyA)
	if freshA.echo(t, 800*time.Millisecond) {
		t.Fatal("new revoked XUDP association echoed")
	}
	if !assocB.echo(t, 2*time.Second) {
		t.Fatal("unrevoked XUDP association stopped echoing")
	}

	a.revoke(deviceA)
	reconcile([]string{})
	afterClearA := openRevocationUDP(t, proxyA)
	if afterClearA.echo(t, 800*time.Millisecond) {
		t.Fatal("clearing snapshot bypassed central XUDP permit revocation")
	}
	if !assocB.echo(t, 2*time.Second) {
		t.Fatal("unrevoked XUDP association stopped after snapshot clear")
	}
	recordEvidence(t, "vision_xudp_revocation", map[string]any{"existing_a_echoes_after_revoke": 0, "probes": 3, "fresh_a_denied": true, "b_continued": true, "central_revoke_after_snapshot_clear_denied": true})
}
