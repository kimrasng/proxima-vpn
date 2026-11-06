package connectionlog

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestPipeline_ReplaysIdenticalHTTPBatchAfterAgentRestartAndDisable(t *testing.T) {
	// Given a real exporter, private durable directory, and authenticated wire server.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, api := newWireAPI(t)
	obs, path := observer(t, ctx, "xray")
	dir := t.TempDir()
	m := newManager(t, api, dir)
	must(t, m.Register(NewUnixSource("xray", path)))
	must(t, m.Poll(ctx))
	stop := startSource(t, ctx, m, "xray")
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" })
	tunnel, err := obs.NewTunnel(connectionhistory.TunnelInfo{Protocol: "vless", InboundTag: "reality",
		LabelKind: "xray_email", Label: "authenticated-user"})
	must(t, err)
	host, port := "example.test", 443
	flow, err := obs.NewFlow(connectionhistory.FlowInfo{Tunnel: tunnel,
		Destination: nodeprov.HistoryDestination{RequestedHost: &host, RequestedPort: &port, Transport: "tcp"}})
	must(t, err)
	must(t, flow.Observe(17, 29))
	must(t, flow.Close(connectionhistory.End{Reason: "eof", Semantics: "local_owner_closed"}))
	must(t, tunnel.Close(connectionhistory.End{Reason: "eof", Semantics: "local_owner_closed"}))
	last := obs.Hello().LastSequence
	waitStatus(t, m, func(s Status) bool { return s.Producers[0].LastDurableSequence == last })
	wire.mu.Lock()
	wire.fail = true
	wire.mu.Unlock()

	// When HTTP is unavailable, disable crosses a hot barrier and the agent restarts.
	if err := m.UploadOnce(ctx); err == nil {
		t.Fatal("outage was acknowledged")
	}
	wire.mu.Lock()
	wire.fail, wire.cfg.Enabled, wire.cfg.Revision = false, false, 2
	wire.mu.Unlock()
	must(t, m.Poll(ctx))
	waitStatus(t, m, func(s Status) bool { return !s.Enabled && s.AppliedRevision == 2 })
	stop()
	boot, sequence := obs.BootID(), obs.Hello().LastSequence
	checkpointJSON, err := json.Marshal(m.out.disk.Sources["xray"])
	must(t, err)
	t.Logf("durable checkpoint before restart: %s", checkpointJSON)
	must(t, m.Close())
	m = newManager(t, api, dir)
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(NewUnixSource("xray", path)))
	must(t, m.Poll(ctx))
	stop = startSource(t, ctx, m, "xray")
	defer stop()
	waitStatus(t, m, func(s Status) bool { return s.State == "off" && s.AppliedRevision == 2 })
	if obs.BootID() != boot || obs.Hello().LastSequence != sequence {
		t.Fatal("reconnect toggled or restarted the surviving runtime")
	}
	nextEpoch := uuid(t)
	wire.mu.Lock()
	wire.cfg.Enabled, wire.cfg.Revision, wire.cfg.CollectionEpoch = true, 3, &nextEpoch
	wire.mu.Unlock()
	must(t, m.Poll(ctx))
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" && s.AppliedRevision == 3 })
	wire.mu.Lock()
	wire.invalid = true
	wire.mu.Unlock()
	if err := m.UploadOnce(ctx); err == nil {
		t.Fatal("mismatching HTTP ACK reclaimed data")
	}
	wire.mu.Lock()
	wire.invalid, wire.expired = false, true
	wire.mu.Unlock()
	must(t, m.UploadOnce(ctx))

	// Then replay is byte-identical and both old-epoch and disabled records drain.
	wire.mu.Lock()
	if len(wire.bodies) != 3 || !bytes.Equal(wire.bodies[0], wire.bodies[1]) || !bytes.Equal(wire.bodies[1], wire.bodies[2]) {
		t.Error("HTTP retry changed immutable batch")
	}
	wire.mu.Unlock()
	must(t, m.UploadOnce(ctx))
	if m.Status().QueueBytes != 0 {
		t.Fatal("acknowledged history not reclaimed")
	}
}
