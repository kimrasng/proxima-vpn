package main

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

// localBandwidth enforces each device's plan speed on this node without a
// control-plane round trip per chunk. Direct-mode traffic therefore never
// waits on the panel: the panel only distributes the plan rate through the
// Xray config (limit-tier inbound tags), and the node meters bytes locally.
//
// Buckets are per device UUID and direction, shared by every stream of that
// device on this node. A device attached to several Exits at once is metered
// independently on each Exit.
type localBandwidth struct {
	mu      sync.Mutex
	now     func() time.Time
	rates   map[string]int // device UUID -> Mbps; 0 = unlimited
	known   bool           // false until the first valid config generation
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	rate   float64 // bytes per second
	burst  float64
	tokens float64
	last   time.Time
}

func newLocalBandwidth() *localBandwidth {
	return &localBandwidth{now: time.Now, rates: map[string]int{}, buckets: map[string]*tokenBucket{}}
}

// SetRates replaces the per-device plan rates. Nil marks the rates unknown,
// which denies all metered traffic until a valid generation arrives.
func (l *localBandwidth) SetRates(rates map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rates == nil {
		l.rates, l.known = map[string]int{}, false
		l.buckets = map[string]*tokenBucket{}
		return
	}
	next := make(map[string]int, len(rates))
	for id, mbps := range rates {
		next[id] = max(mbps, 0)
	}
	// Drop buckets whose device left or whose rate changed, so a new limit
	// takes effect immediately instead of inheriting an old burst.
	for key, bucket := range l.buckets {
		id := key[:len(key)-2]
		mbps, ok := next[id]
		if !ok || bucketRate(mbps) != bucket.rate {
			delete(l.buckets, key)
		}
	}
	l.rates, l.known = next, true
}

func bucketRate(mbps int) float64 { return float64(mbps) * 125000 } // decimal Mbps -> bytes/s

// Permit implements deviceegress.PermitFunc locally. It never blocks; when the
// bucket is short it returns the wait the caller should sleep before retrying.
func (l *localBandwidth) Permit(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
	if err := ctx.Err(); err != nil {
		return devicebandwidth.PermitResponse{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	mbps, ok := l.rates[uuid]
	if !l.known || !ok {
		// Unknown device or no valid config: fail closed.
		return devicebandwidth.PermitResponse{DeniedReason: "device_not_authorized"}, nil
	}
	if mbps == 0 {
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}
	suffix := ":u"
	if direction == devicebandwidth.Download {
		suffix = ":d"
	}
	key := uuid + suffix
	now := l.now()
	bucket := l.buckets[key]
	if bucket == nil {
		rate := bucketRate(mbps)
		// Same burst policy as the former central limiter.
		burst := math.Max(devicebandwidth.MaxChunk, math.Min(rate/10, 262144))
		bucket = &tokenBucket{rate: rate, burst: burst, tokens: burst, last: now}
		l.buckets[key] = bucket
	}
	if elapsed := now.Sub(bucket.last).Seconds(); elapsed > 0 {
		bucket.tokens = math.Min(bucket.burst, bucket.tokens+elapsed*bucket.rate)
		bucket.last = now
	}
	need := float64(size)
	if bucket.tokens >= need {
		bucket.tokens -= need
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}
	wait := int(math.Ceil((need - bucket.tokens) * 1000 / bucket.rate))
	return devicebandwidth.PermitResponse{RetryAfterMS: min(max(wait, 1), 1000)}, nil
}
