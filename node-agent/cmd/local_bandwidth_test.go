package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestLocalBandwidthNoControlPlaneAndRateBound(t *testing.T) {
	l := newLocalBandwidth()
	clock := time.Unix(1000, 0)
	l.now = func() time.Time { return clock }
	ctx := context.Background()

	// Unknown rates (no valid generation yet) fail closed.
	if r, _ := l.Permit(ctx, "dev-a", devicebandwidth.Download, 100); r.Allowed || r.DeniedReason == "" {
		t.Fatalf("unknown rates must deny, got %+v", r)
	}
	l.SetRates(map[string]int{"dev-a": 8, "dev-free": 0}) // 8 Mbps = 1,000,000 B/s

	if r, _ := l.Permit(ctx, "dev-free", devicebandwidth.Download, devicebandwidth.MaxChunk); !r.Allowed {
		t.Fatal("unlimited device must pass")
	}
	if r, _ := l.Permit(ctx, "stranger", devicebandwidth.Download, 1); r.Allowed || r.DeniedReason == "" {
		t.Fatal("device absent from config must be denied")
	}

	// Simulate 10 s of saturated download; delivered bytes must track rate.
	var delivered int
	end := clock.Add(10 * time.Second)
	for clock.Before(end) {
		r, _ := l.Permit(ctx, "dev-a", devicebandwidth.Download, devicebandwidth.MaxChunk)
		if r.Allowed {
			delivered += devicebandwidth.MaxChunk
			continue
		}
		if r.RetryAfterMS < 1 {
			t.Fatalf("short bucket must give a positive wait, got %+v", r)
		}
		clock = clock.Add(time.Duration(r.RetryAfterMS) * time.Millisecond)
	}
	const rate, burst = 1_000_000, 100_000 // burst = rate/10
	if delivered > rate*10+burst+devicebandwidth.MaxChunk || delivered < rate*10*95/100 {
		t.Fatalf("delivered %d bytes in 10s at 1MB/s cap", delivered)
	}

	// Upload has its own bucket: an exhausted download does not starve it.
	if r, _ := l.Permit(ctx, "dev-a", devicebandwidth.Upload, devicebandwidth.MaxChunk); !r.Allowed {
		t.Fatal("upload must be metered independently of download")
	}

	// Rate change takes effect immediately, without inheriting old burst.
	l.SetRates(map[string]int{"dev-a": 0})
	if r, _ := l.Permit(ctx, "dev-a", devicebandwidth.Download, devicebandwidth.MaxChunk); !r.Allowed {
		t.Fatal("raised-to-unlimited device must pass immediately")
	}
	// Losing the config denies again.
	l.SetRates(nil)
	if r, _ := l.Permit(ctx, "dev-a", devicebandwidth.Download, 1); r.Allowed {
		t.Fatal("cleared rates must deny")
	}
}

func TestDeviceConfigRatesFromLimitTier(t *testing.T) {
	cfg, err := parseDeviceConfig([]byte(`{
	  "inbounds":[{"tag":"vless-reality-limit-20","protocol":"vless","settings":{"clients":[{"id":"d1","email":"d1@proxima"}]}}],
	  "outbounds":[{"tag":"device-egress-d1","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":10086,"users":[{"user":"d1","pass":"materialize-at-node"}]}]}}],
	  "routing":{"rules":[{"type":"field","user":["d1@proxima"],"outboundTag":"device-egress-d1"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.rates["d1"] != 20 {
		t.Fatalf("rate = %d, want 20 from limit tier", cfg.rates["d1"])
	}
}

func TestDeviceConfigSharedPortUsesExplicitRate(t *testing.T) {
	cfg, err := parseDeviceConfig([]byte(`{
	  "proxima_device_rates":{"d1":25},
	  "inbounds":[{"tag":"vless-reality","protocol":"vless","port":20025,"settings":{"clients":[{"id":"d1","email":"d1@proxima"}]}}],
	  "outbounds":[{"tag":"device-egress-d1","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":10086,"users":[{"user":"d1","pass":"materialize-at-node"}]}]}}],
	  "routing":{"rules":[{"type":"field","user":["d1@proxima"],"outboundTag":"device-egress-d1"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.rates["d1"] != 25 {
		t.Fatalf("shared-port rate = %d, want 25 Mbps", cfg.rates["d1"])
	}
}

func TestSharedPortRateAppliedToRealSOCKSTraffic(t *testing.T) {
	limiter := newLocalBandwidth()
	limited := make(chan struct{}, 1)
	permit := func(ctx context.Context, id string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
		result, err := limiter.Permit(ctx, id, direction, size)
		if err == nil && !result.Allowed && result.RetryAfterMS > 0 {
			select {
			case limited <- struct{}{}:
			default:
			}
		}
		return result, err
	}
	server := deviceegress.NewWithDialer(permit, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	egress := newDeviceEgress(server.SetCredentials)
	egress.setRates = limiter.SetRates
	canonical := strings.Replace(limitedDeviceConfig, `{"inbounds"`, `{"proxima_device_rates":{"uuid-a":1},"inbounds"`, 1)
	if _, err := egress.TransformConfig([]byte(canonical)); err != nil {
		t.Fatal(err)
	}
	password := egress.passwords["device-egress-uuid-a"]
	conn, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	exchange := func(request []byte, response []byte) {
		t.Helper()
		if _, err := conn.Write(request); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, response); err != nil {
			t.Fatal(err)
		}
	}
	response := make([]byte, 2)
	exchange([]byte{5, 1, 2}, response)
	if !bytes.Equal(response, []byte{5, 2}) {
		t.Fatalf("method = %v", response)
	}
	auth := append([]byte{1, byte(len("uuid-a"))}, []byte("uuid-a")...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, []byte(password)...)
	exchange(auth, response)
	if !bytes.Equal(response, []byte{1, 0}) {
		t.Fatalf("auth = %v", response)
	}
	connect := make([]byte, 10)
	exchange([]byte{5, 1, 0, 1, 203, 0, 113, 1, 0, 80}, connect)
	if connect[1] != 0 {
		t.Fatalf("connect = %v", connect)
	}
	payload := make([]byte, 200000)
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-limited:
	case <-time.After(3 * time.Second):
		t.Fatal("real SOCKS traffic did not reach the configured rate limit")
	}
}

func TestDeviceConfigExplicitRateOverridesCompatibilityTier(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rates   string
		want    int
		invalid bool
	}{
		{"high speed", `{"d1":5000}`, 5000, false},
		{"unlimited", `{"d1":0}`, 0, false},
		{"missing", `{}`, 0, true},
		{"null", `{"d1":null}`, 0, true},
		{"negative", `{"d1":-1}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := `{"proxima_device_rates":` + tc.rates + `,
			  "inbounds":[{"tag":"vless-reality-limit-2000","protocol":"vless","settings":{"clients":[{"id":"d1","email":"d1@proxima"}]}}],
			  "outbounds":[{"tag":"device-egress-d1","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":10086,"users":[{"user":"d1","pass":"materialize-at-node"}]}]}}],
			  "routing":{"rules":[{"type":"field","user":["d1@proxima"],"outboundTag":"device-egress-d1"}]}}`
			cfg, err := parseDeviceConfig([]byte(data))
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid device rate accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.rates["d1"] != tc.want {
				t.Fatalf("rate = %d, want %d", cfg.rates["d1"], tc.want)
			}
		})
	}
}
