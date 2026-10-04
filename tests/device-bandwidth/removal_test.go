package bandwidth_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"
)

// This isolates upstream Xray's RemoveUser semantics from egress revocation.
// The limiter remains authorized and running while the API removes the inbound user.
func removalFixture(t *testing.T, binary string, xudp bool) (string, string, int) {
	t.Helper()
	a := newAuthority()
	a.add(deviceA, rateA, burst)
	tcp := payloadServer(t)
	s := limiter(t, a, tcp, udpEcho(t), nil)
	private, public := realityKeys(t)
	serverPort, apiPort := reserveTCPPort(t), reserveTCPPort(t)
	cfg := generatedServer(t, serverPort, private, tcp, s)
	cfg["api"] = map[string]any{"tag": "api", "services": []string{"HandlerService"}}
	cfg["inbounds"] = append(cfg["inbounds"].([]any), map[string]any{
		"tag": "api-in", "listen": "127.0.0.1", "port": apiPort,
		"protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"},
	})
	routing := cfg["routing"].(map[string]any)
	routing["rules"] = append(routing["rules"].([]any), map[string]any{
		"type": "field", "inboundTag": []string{"api-in"}, "outboundTag": "api",
	})
	startXray(t, binary, "removal-server", cfg, serverPort)
	clientPort := reserveTCPPort(t)
	client := xrayClientConfig(clientPort, serverPort, deviceA, public)
	if xudp {
		outbound := client["outbounds"].([]any)[0].(map[string]any)
		outbound["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)["flow"] = "xtls-rprx-vision-udp443"
		outbound["mux"] = map[string]any{"enabled": false, "xudpConcurrency": 8, "xudpProxyUDP443": "allow"}
	}
	startXray(t, binary, "removal-client", client, clientPort)
	return fmt.Sprintf("127.0.0.1:%d", clientPort), fmt.Sprintf("127.0.0.1:%d", apiPort), apiPort
}

func removeInboundUser(t *testing.T, binary, api string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "api", "rmu", "-server="+api, "-tag=vless-reality", deviceA+"@proxima")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("RemoveUser failed: %v: %s", err, sanitize(string(output)))
	}
}

func TestRealXrayRemoveUserEstablishedVisionTCP(t *testing.T) {
	binary := requireXray(t)
	proxy, api, _ := removalFixture(t, binary, false)
	c := openPayload(t, proxy, "", "")
	defer c.Close()
	buf := make([]byte, 16*1024)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c.Read(buf); n == 0 || err != nil {
		t.Fatalf("TCP session not carrying payload before removal: %d %v", n, err)
	}
	removeInboundUser(t, binary, api)
	// A fresh handshake must be rejected, while we separately observe whether the
	// already-established Vision tunnel still forwards actual response bytes.
	rejectPayload(t, proxy)
	deadline := time.Now().Add(1500 * time.Millisecond)
	var forwarded int
	var finalErr error
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		n, err := c.Read(buf)
		forwarded += n
		if err != nil {
			finalErr = err
			break
		}
	}
	recordEvidence(t, "remove_user_tcp_existing", map[string]any{"bytes_after_remove": forwarded, "observation_ms": 1500, "read_error": fmt.Sprint(finalErr), "fresh_connection_rejected": true})
	t.Logf("established TCP stream forwarded %d bytes in 1.5s after RemoveUser (end: %v)", forwarded, finalErr)
}

func TestRealXrayRemoveUserEstablishedVisionXUDP(t *testing.T) {
	binary := requireXray(t)
	proxy, api, _ := removalFixture(t, binary, true)
	control, relay := socksConnect(t, proxy, "", "", 3, "0.0.0.0", 0)
	defer control.Close()
	if relay.IP.IsUnspecified() {
		relay.IP = net.ParseIP("127.0.0.1")
	}
	if !relay.IP.IsLoopback() {
		t.Fatalf("UDP relay not loopback: %v", relay)
	}
	conn, err := net.DialUDP("udp4", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := []byte("existing-xudp-session-probe")
	frame := udpFrame(payload)
	probe := func() bool {
		_, err := conn.Write(frame)
		if err != nil {
			return false
		}
		_ = conn.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
		buf := make([]byte, 2048)
		n, err := conn.Read(buf)
		if err != nil {
			return false
		}
		got, err := udpPayload(buf[:n])
		return err == nil && bytes.Equal(got, payload)
	}
	if !probe() {
		t.Fatal("XUDP echo not established before removal")
	}
	removeInboundUser(t, binary, api)
	// Repeat on the SAME UDP association; no new SOCKS/VLESS authentication.
	replies := 0
	for i := 0; i < 3; i++ {
		if probe() {
			replies++
		}
		time.Sleep(50 * time.Millisecond)
	}
	recordEvidence(t, "remove_user_xudp_existing", map[string]any{"echo_replies_after_remove": replies, "probes_after_remove": 3})
	t.Logf("established XUDP association echoed %d/3 datagrams after RemoveUser", replies)
}
