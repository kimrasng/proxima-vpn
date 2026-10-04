package bandwidth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

const (
	deviceA       = "b0808f3d-6476-4ee0-a193-a4b0a99c2501"
	deviceB       = "b0808f3d-6476-4ee0-a193-a4b0a99c2502"
	rateA   int64 = 128 * 1024
	rateB   int64 = 256 * 1024
	burst   int64 = 64 * 1024
	window        = 8 * time.Second
)

func fixtureCredentials() []devicebandwidth.Credential {
	return []devicebandwidth.Credential{{UUID: deviceA, Password: "fixture-password-a"}, {UUID: deviceB, Password: "fixture-password-b"}}
}

func limiter(t *testing.T, a *fakeAuthority, tcp, udp string, count *atomic.Int64) *deviceegress.Server {
	t.Helper()
	s := deviceegress.NewWithDialer(a.permit, mappedDial(t, tcp, udp, count))
	credentials := fixtureCredentials()
	for _, c := range credentials {
		rememberSecret(c.UUID)
		rememberSecret(c.Password)
	}
	if err := s.SetCredentials(credentials); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func generatedServer(t *testing.T, port int, privateKey, camouflage string, s *deviceegress.Server) map[string]any {
	t.Helper()
	generator := os.Getenv("BANDWIDTH_GENERATOR")
	if generator == "" {
		t.Skip("run via run.sh to build the real API generator adapter")
	}
	clients := []any{}
	for _, c := range fixtureCredentials() {
		clients = append(clients, map[string]any{"id": c.UUID, "email": c.UUID + "@proxima", "flow": "xtls-rprx-vision"})
	}
	base := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"tag": "vless-reality", "listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings": map[string]any{"clients": clients, "decryption": "none"},
			"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{
				"dest": camouflage, "serverNames": []string{"bandwidth.test"}, "privateKey": privateKey, "shortIds": []string{"0123456789abcdef"},
			}},
		}},
	}
	input, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, generator)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("real generator: %v %s", err, sanitize(stderr.String()))
	}
	var cfg map[string]any
	if err := json.Unmarshal(output, &cfg); err != nil {
		t.Fatal(err)
	}
	// Match the generated servers/users shape exactly. Materialize only the
	// declaration password and ephemeral listener port, as node runtime must.
	_, portString, err := net.SplitHostPort(s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	limiterPort, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range cfg["outbounds"].([]any) {
		ob := v.(map[string]any)
		if ob["protocol"] != "socks" {
			continue
		}
		settings := ob["settings"].(map[string]any)
		servers := settings["servers"].([]any)
		if len(servers) != 1 {
			t.Fatal("expected generated one-server SOCKS declaration")
		}
		server := servers[0].(map[string]any)
		server["port"] = limiterPort
		users := server["users"].([]any)
		if len(users) != 1 {
			t.Fatal("expected generated one-user SOCKS declaration")
		}
		user := users[0].(map[string]any)
		found := false
		for _, c := range fixtureCredentials() {
			if user["user"] == c.UUID {
				user["pass"] = c.Password
				found = true
			}
		}
		if !found {
			t.Fatal("unknown generated SOCKS username")
		}
	}
	recordEvidence(t, "generated_schema", map[string]any{"transformer": "services.WithDeviceEgressRouting", "materialized_fields": []string{"SOCKS port", "SOCKS password"}, "original_outbound_count": len(cfg["outbounds"].([]any))})
	return cfg
}

func TestRealXrayVisionPerDeviceAndSharedAcrossExits(t *testing.T) {
	binary := requireXray(t)
	version, err := exec.Command(binary, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	recordEvidence(t, "xray_version", sanitize(string(version)))
	begin := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(begin) }) })
	tcp := payloadServerWithGate(t, begin)
	a := newAuthority()
	a.add(deviceA, rateA, burst)
	a.add(deviceB, rateB, burst)
	var dials atomic.Int64
	private, public := realityKeys(t)
	var proxies [2][2]string
	for exit := 0; exit < 2; exit++ {
		s := limiter(t, a, tcp, "", &dials)
		serverPort := reserveTCPPort(t)
		startXray(t, binary, fmt.Sprintf("server-%d", exit), generatedServer(t, serverPort, private, tcp, s), serverPort)
		for device, id := range []string{deviceA, deviceB} {
			port := reserveTCPPort(t)
			startXray(t, binary, fmt.Sprintf("client-%d-%d", exit, device), xrayClientConfig(port, serverPort, id, public), port)
			proxies[exit][device] = fmt.Sprintf("127.0.0.1:%d", port)
		}
	}
	// Two sessions per device on each exit: four independently connected Vision
	// TLS streams share ONE per-device central bucket across both limiter instances.
	streams := [2][]net.Conn{}
	for device := 0; device < 2; device++ {
		for exit := 0; exit < 2; exit++ {
			for session := 0; session < 2; session++ {
				streams[device] = append(streams[device], openPayload(t, proxies[exit][device], "", ""))
			}
		}
	}
	var wg sync.WaitGroup
	rates := []int64{rateA, rateB}
	results := [2]trafficResult{}
	for device := 0; device < 2; device++ {
		wg.Add(1)
		go func(device int) {
			defer wg.Done()
			// Explicit tolerance: central burst + 2 exits × 1 maximum granted
			// chunk; no per-session extra burst is permitted. TLS overhead counts
			// against permits, so measured application bytes remain below ceiling.
			results[device] = measure(t, streams[device], window, rates[device], burst+2*devicebandwidth.MaxChunk, begin)
		}(device)
	}
	release.Do(func() { close(begin) })
	wg.Wait()
	for device := 0; device < 2; device++ {
		recordEvidence(t, fmt.Sprintf("device_%d_four_sessions_two_exits", device), results[device])
		for _, c := range streams[device] {
			c.Close()
		}
	}
	if results[1].Bytes < results[0].Bytes*3/2 {
		t.Error("different devices did not receive independent configured rates")
	}
	if dials.Load() != 8 {
		t.Errorf("wanted all 8 sessions to traverse injected limiter dial; got %d", dials.Load())
	}
	recordEvidence(t, "transport_dials", dials.Load())
}

func TestControlledSOCKSUDPSharedDirectionalBudget(t *testing.T) {
	a := newAuthority()
	a.add(deviceA, rateA, burst)
	s := limiter(t, a, "", udpEcho(t), nil)
	result := udpProbe(t, s.Addr().String(), deviceA, "fixture-password-a", window, rateA, burst)
	recordEvidence(t, "udp_echo", result)
}

func assertClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 32*1024)
	var residual int64
	for {
		n, err := c.Read(buf)
		residual += int64(n)
		if residual > 2*1024*1024 {
			t.Fatal("revoked/failed session kept forwarding past drain allowance")
		}
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("revoked/failed session remained open")
			}
			return
		}
	}
}

func TestPermitErrorAndCredentialRevocationCloseExisting(t *testing.T) {
	for _, scenario := range []string{"permit_api_error", "credential_removed", "credential_rotated", "central_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			a := newAuthority()
			a.add(deviceA, rateA, burst)
			s := limiter(t, a, payloadServer(t), "", nil)
			c := openPayload(t, s.Addr().String(), deviceA, "fixture-password-a")
			// Establish actual bytes before the failure/revocation is triggered.
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			first := make([]byte, 1024)
			if _, err := c.Read(first); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "permit_api_error":
				a.fail()
			case "credential_removed":
				if err := s.SetCredentials(nil); err != nil {
					t.Fatal(err)
				}
			case "credential_rotated":
				if err := s.SetCredentials([]devicebandwidth.Credential{{UUID: deviceA, Password: "new-fixture-password"}}); err != nil {
					t.Fatal(err)
				}
			case "central_revoked":
				a.revoke(deviceA)
			}
			assertClosed(t, c)
			recordEvidence(t, "existing_connection_closed", true)
		})
	}
}

func TestRealXrayLimiterDownNeverFallsBackToFreedom(t *testing.T) {
	binary := requireXray(t)
	a := newAuthority()
	a.add(deviceA, rateA, burst)
	var dials atomic.Int64
	tcp := payloadServer(t)
	s := limiter(t, a, tcp, "", &dials)
	private, public := realityKeys(t)
	serverPort := reserveTCPPort(t)
	cfg := generatedServer(t, serverPort, private, tcp, s)
	// A tripwire makes a routing bypass observable locally: the generated direct
	// Freedom outbound remains present and, ONLY in this test, redirects any
	// unintended fallback to the working local payload. Production config has no
	// such redirect. A bypass would complete TLS and fail rejectPayload below.
	for _, raw := range cfg["outbounds"].([]any) {
		ob := raw.(map[string]any)
		if ob["tag"] == "direct" {
			ob["settings"] = map[string]any{"redirect": tcp}
		}
	}
	startXray(t, binary, "outage-server", cfg, serverPort)
	port := reserveTCPPort(t)
	startXray(t, binary, "outage-client", xrayClientConfig(port, serverPort, deviceA, public), port)
	proxy := fmt.Sprintf("127.0.0.1:%d", port)
	c := openPayload(t, proxy, "", "")
	c.Close()
	before := dials.Load()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	rejectPayload(t, proxy)
	if dials.Load() != before {
		t.Error("closed limiter still dialled transport")
	}
	recordEvidence(t, "closed_limiter_no_freedom_fallback", true)
}
