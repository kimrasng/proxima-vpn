package main

import (
	"context"
	"testing"
	"time"

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
