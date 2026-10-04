package bandwidth_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

// fakeAuthority is one central authority shared by *all* limiter instances.
// It grants exact byte counts in a per-device, per-direction bucket; there
// are no local exit/device permit caches.
// Redis/real-API semantics are intentionally a separate verification boundary.
type fakeAuthority struct {
	mu      sync.Mutex
	devices map[string]*fakeDevice
	failed  bool
	granted map[string]int64
}
type fakeDevice struct {
	rate    int64
	burst   int64
	tokens  map[devicebandwidth.Direction]float64
	last    map[devicebandwidth.Direction]time.Time
	revoked bool
}

func newAuthority() *fakeAuthority {
	return &fakeAuthority{devices: map[string]*fakeDevice{}, granted: map[string]int64{}}
}
func (a *fakeAuthority) add(uuid string, rate, burst int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.devices[uuid] = &fakeDevice{rate: rate, burst: burst,
		tokens: map[devicebandwidth.Direction]float64{devicebandwidth.Upload: float64(burst), devicebandwidth.Download: float64(burst)},
		last:   map[devicebandwidth.Direction]time.Time{devicebandwidth.Upload: time.Now(), devicebandwidth.Download: time.Now()}}
}
func (a *fakeAuthority) revoke(uuid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.devices[uuid].revoked = true
}
func (a *fakeAuthority) fail() { a.mu.Lock(); defer a.mu.Unlock(); a.failed = true }

func (a *fakeAuthority) permit(ctx context.Context, uuid string, direction devicebandwidth.Direction, wanted int) (devicebandwidth.PermitResponse, error) {
	if err := ctx.Err(); err != nil {
		return devicebandwidth.PermitResponse{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed {
		return devicebandwidth.PermitResponse{}, errors.New("synthetic API unavailable")
	}
	d, ok := a.devices[uuid]
	if !ok || d.revoked {
		return devicebandwidth.PermitResponse{}, errors.New("synthetic device revoked")
	}
	if wanted < 1 || wanted > devicebandwidth.MaxChunk {
		return devicebandwidth.PermitResponse{}, errors.New("invalid chunk")
	}
	if direction != devicebandwidth.Upload && direction != devicebandwidth.Download {
		return devicebandwidth.PermitResponse{}, errors.New("invalid direction")
	}
	now := time.Now()
	d.tokens[direction] += now.Sub(d.last[direction]).Seconds() * float64(d.rate)
	if d.tokens[direction] > float64(d.burst) {
		d.tokens[direction] = float64(d.burst)
	}
	d.last[direction] = now
	if d.tokens[direction] >= float64(wanted) {
		d.tokens[direction] -= float64(wanted)
		a.granted[uuid] += int64(wanted)
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}
	wait := time.Duration((float64(wanted) - d.tokens[direction]) / float64(d.rate) * float64(time.Second))
	return devicebandwidth.PermitResponse{RetryAfterMS: int((wait + time.Millisecond - 1) / time.Millisecond)}, nil
}
