package connectionlog

import (
	"context"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestManager_ProducerGapDegradesCurrentEpochUntilFreshEnable(t *testing.T) {
	// Given an actively collected real exporter.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, api := newWireAPI(t)
	obs, path := observer(t, ctx, "xray")
	m := newManager(t, api, t.TempDir())
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(NewUnixSource("xray", path)))
	must(t, m.Poll(ctx))
	stop := startSource(t, ctx, m, "xray")
	defer stop()
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" })
	// When the producer publishes a real capture-loss gap in the current epoch.
	must(t, obs.ReportGap(nodeprov.HistoryGap{ProducerID: "xray",
		ObserverBootID: obs.BootID(), Reason: "capture_drop"}))
	// Then collection cannot claim complete health, even after an idempotent poll.
	waitStatus(t, m, func(s Status) bool { return s.State == "degraded" && s.ErrorCode == "capture_drop" })
	must(t, m.Poll(ctx))
	if m.Status().State != "degraded" {
		t.Fatal("idempotent config retry erased producer loss health")
	}
	wire.mu.Lock()
	wire.cfg.Enabled, wire.cfg.Revision = false, 2
	wire.mu.Unlock()
	must(t, m.Poll(ctx))
	waitStatus(t, m, func(s Status) bool { return s.State == "off" && s.AppliedRevision == 2 })
	epoch := uuid(t)
	wire.mu.Lock()
	wire.cfg.Enabled, wire.cfg.Revision, wire.cfg.CollectionEpoch = true, 3, &epoch
	wire.mu.Unlock()
	must(t, m.Poll(ctx))
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" && s.AppliedRevision == 3 })
}
