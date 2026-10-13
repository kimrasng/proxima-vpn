package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

// TestSourceEgressLive owns every listener and Xray subprocess it touches.
// Run the compiled test inside a disposable Linux container (not production):
// PROXIMA_SOURCE_EGRESS_LIVE=1 ./cmd.test -test.run '^TestSourceEgressLive$'
// Xray must be 26.7.28 at /usr/local/bin/xray, or PROXIMA_SOURCE_EGRESS_XRAY.
// Plain VLESS TCP supplies the authenticated UUID@proxima identity; raw clients
// bind .2 and .3 in this namespace, avoiding Docker port-forward source NAT.
func TestSourceEgressLive(t *testing.T) {
	if os.Getenv("PROXIMA_SOURCE_EGRESS_LIVE") != "1" {
		t.Skip("requires opt-in disposable container: PROXIMA_SOURCE_EGRESS_LIVE=1")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("run in a disposable Linux container with distinct loopback sources")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := os.Getenv("PROXIMA_SOURCE_EGRESS_XRAY")
	if binary == "" {
		binary = "/usr/local/bin/xray"
	}
	version, err := exec.CommandContext(ctx, binary, "version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "Xray 26.7.28 ") {
		t.Fatalf("requires Xray 26.7.28: %s (%v)", version, err)
	}

	sink, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	go func() {
		for {
			conn, err := sink.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	const uuid = "8f1c7e0a-1b2c-4d3e-9f40-112233445566"
	// Real SOCKS authentication, authorization and stream revocation, with only
	// the public destination transport redirected to our local echo sink.
	server := deviceegress.NewWithDialer(func(_ context.Context, id string, _ devicebandwidth.Direction, _ int) (devicebandwidth.PermitResponse, error) {
		return devicebandwidth.PermitResponse{Allowed: id == uuid}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:80" {
			return nil, fmt.Errorf("unexpected destination %s", address)
		}
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, sink.Addr().String())
	})
	if err := server.Start(ctx, devicebandwidth.ListenAddress); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	egress := newDeviceEgress(server.SetCredentials)
	apiPort, ingressPort := sourceEgressLivePort(t), sourceEgressLivePort(t)
	canonical := func(sources []string) []byte {
		outbounds := []any{map[string]any{"tag": "block", "protocol": "blackhole"}}
		rules := []any{map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"}}
		for _, source := range sources {
			key := strings.Repeat("2", 32)
			if source == "127.0.0.3" {
				key = strings.Repeat("3", 32)
			}
			tag := devicebandwidth.OutboundPrefix + uuid + "~" + key
			outbounds = append(outbounds, map[string]any{"tag": tag, "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": devicebandwidth.Port, "users": []any{map[string]any{"user": uuid, "pass": deviceEgressPlaceholder}}}}}})
			rules = append(rules, map[string]any{"type": "field", "user": []string{uuid + "@proxima"}, "inboundTag": []string{"vless-in"}, "source": []string{source + "/32"}, "outboundTag": tag})
		}
		if len(sources) == 0 {
			rules = append(rules, map[string]any{"type": "field", "user": []string{uuid + "@proxima"}, "outboundTag": "block"})
		}
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"vless-in"}, "outboundTag": "block"})
		cfg := map[string]any{
			"log": map[string]any{"loglevel": "warning"},
			"api": map[string]any{"tag": "api", "services": []string{"HandlerService", "RoutingService"}},
			"inbounds": []any{
				map[string]any{"tag": "api", "listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1"}},
				map[string]any{"tag": "vless-in", "listen": "127.0.0.1", "port": ingressPort, "protocol": "vless", "settings": map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": uuid, "email": uuid + "@proxima"}}}, "streamSettings": map[string]any{"network": "tcp", "security": "none"}},
			},
			"outbounds": outbounds, "routing": map[string]any{"rules": rules},
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	before, err := egress.TransformConfig(canonical([]string{"127.0.0.2", "127.0.0.3"}))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(config, before, 0600); err != nil {
		t.Fatal(err)
	}
	logs, err := os.Create(filepath.Join(dir, "xray.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	process := exec.CommandContext(ctx, binary, "run", "-config", config)
	process.Stdout, process.Stderr = logs, logs
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	pid := process.Process.Pid
	defer func() {
		_ = process.Process.Kill()
		_ = process.Wait()
		if t.Failed() {
			data, _ := os.ReadFile(logs.Name())
			t.Logf("Xray log:\n%s", data)
		}
	}()
	apiAddr := fmt.Sprintf("127.0.0.1:%d", apiPort)
	ready := false
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; {
		conn, err := net.DialTimeout("tcp4", apiAddr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("Xray API did not start")
	}
	client, err := xray.NewStatsClient(apiAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	ingress := fmt.Sprintf("127.0.0.1:%d", ingressPort)
	removed := sourceEgressLiveConnect(t, ingress, "127.0.0.2", uuid)
	retained := sourceEgressLiveConnect(t, ingress, "127.0.0.3", uuid)
	sourceEgressLiveEcho(t, removed, "removed-before")
	sourceEgressLiveEcho(t, retained, "retained-before")
	after, err := egress.TransformConfig(canonical([]string{"127.0.0.3"}))
	if err != nil {
		t.Fatal(err)
	}
	rpcCtx, stop := context.WithTimeout(ctx, 8*time.Second)
	defer stop()
	if !syncDeviceTopology(rpcCtx, client, before, after) {
		t.Fatal("live sync failed (restart is not allowed)")
	}
	_ = removed.SetReadDeadline(time.Now().Add(5 * time.Second))
	var one [1]byte
	if _, err := removed.Read(one[:]); err == nil {
		t.Fatal("removed source stream stayed open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("removed stream did not close: %v", err)
	}
	sourceEgressLiveEcho(t, retained, "retained-after")
	fresh := sourceEgressLiveConnect(t, ingress, "127.0.0.3", uuid)
	sourceEgressLiveEcho(t, fresh, "retained-fresh")
	rejected := sourceEgressLiveConnect(t, ingress, "127.0.0.2", uuid)
	_ = rejected.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = rejected.Write([]byte("must-not-echo"))
	if _, err := rejected.Read(one[:]); err == nil {
		t.Fatal("removed source reconnect forwarded traffic")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("reconnect was not promptly rejected: %v", err)
	}
	// Signal 0 checks the original process is still alive, not merely that our
	// exec.Cmd retained its original PID after a failed live update.
	if process.Process.Pid != pid {
		t.Fatal("Xray PID changed")
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
		t.Fatalf("original Xray PID no longer alive: %v", err)
	}
	sourceEgressLiveEcho(t, retained, "retained-final")
	denyOnly, err := egress.TransformConfig(canonical(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !syncDeviceTopology(rpcCtx, client, after, denyOnly) {
		t.Fatal("last-source live removal failed (restart is not allowed)")
	}
	for _, conn := range []net.Conn{retained, fresh} {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Read(one[:]); err == nil {
			t.Fatal("last-source existing stream stayed open")
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatalf("last-source stream did not close: %v", err)
		}
	}
	for _, source := range []string{"127.0.0.2", "127.0.0.3"} {
		conn := sourceEgressLiveConnect(t, ingress, source, uuid)
		_, _ = conn.Write([]byte("last-source-must-not-echo"))
		if _, err := conn.Read(one[:]); err == nil {
			t.Fatalf("denied source %s forwarded traffic", source)
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatalf("denied source %s not promptly rejected: %v", source, err)
		}
	}
	if process.Process.Pid != pid {
		t.Fatal("Xray PID changed after last-source removal")
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
		t.Fatalf("original Xray PID died after last-source removal: %v", err)
	}
}

func sourceEgressLivePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// A minimal VLESS TCP request avoids additional client binaries/dependencies.
// The response header is consumed by Echo, since a denied route may close
// before sending it. Each application stream is a separate TCP connection.
func sourceEgressLiveConnect(t *testing.T, ingress, source, uuid string) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{Timeout: 3 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}).Dial("tcp4", ingress)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	id, err := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	request := append([]byte{0}, id...)
	// addon length 0, TCP command 1, port 80, IPv4 address type 1.
	request = append(request, 0, 1, 0, 80, 1, 93, 184, 216, 34)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	return &sourceEgressLiveStream{Conn: conn}
}

type sourceEgressLiveStream struct {
	net.Conn
	headerRead bool
}

func (s *sourceEgressLiveStream) Read(p []byte) (int, error) {
	if !s.headerRead {
		var header [2]byte
		if _, err := io.ReadFull(s.Conn, header[:]); err != nil {
			return 0, err
		}
		if header[0] != 0 {
			return 0, fmt.Errorf("unexpected VLESS response version %d", header[0])
		}
		if _, err := io.CopyN(io.Discard, s.Conn, int64(header[1])); err != nil {
			return 0, err
		}
		s.headerRead = true
	}
	return s.Conn.Read(p)
}

func sourceEgressLiveEcho(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != payload {
		t.Fatalf("echo %q: got %q: %v", payload, got, err)
	}
}
