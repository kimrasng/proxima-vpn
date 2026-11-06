package connectionhistory

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestCollectionScopeSocketAppliedOFFWaitsForReset(t *testing.T) {
	// The collector is connected before controls trigger state changes.
	o, path := socketFixture(t)
	tunnel, err := o.NewTunnel(TunnelInfo{Protocol: "vless", InboundTag: "udp"})
	check(t, err)
	flows := make(map[string]*Flow)
	resetDone := make(chan struct{})
	signaled := false
	scope := o.NewCollectionScope(func() {
		clear(flows)
		if !signaled {
			signaled = true
			close(resetDone)
		}
	})
	conn, err := net.Dial("unix", path)
	check(t, err)
	defer conn.Close()
	check(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	check(t, err)
	if _, err := nodeprov.DecodeHistoryControl(line); err != nil {
		t.Fatal(err)
	}
	send := func(frame nodeprov.HistoryControl) {
		t.Helper()
		data, err := encodeControl(frame)
		check(t, err)
		_, err = conn.Write(data)
		check(t, err)
	}
	receiveApplied := func() ([]nodeprov.HistoryEvent, nodeprov.HistoryApplied) {
		t.Helper()
		var events []nodeprov.HistoryEvent
		for {
			line, err := reader.ReadBytes('\n')
			check(t, err)
			if frame, err := nodeprov.DecodeHistoryControl(line); err == nil {
				applied, ok := frame.(nodeprov.HistoryApplied)
				if !ok {
					t.Fatalf("unexpected control frame: %s", line)
				}
				return events, applied
			}
			event, err := nodeprov.DecodeHistoryEvent(line)
			check(t, err)
			events = append(events, event)
		}
	}
	send(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.boot})
	send(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: ptr(epochOne),
		Enabled: true, Bindings: []nodeprov.HistoryBinding{}})
	receiveApplied()
	send(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 2, CollectionEpoch: ptr(epochOne),
		Enabled: true, Bindings: []nodeprov.HistoryBinding{}})
	receiveApplied()
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		f, err := g.NewFlow(scopedInfo(tunnel, "socket.test"))
		if err != nil {
			return err
		}
		flows["socket.test"] = f
		return g.Observe(f, 19, 7)
	}))
	send(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 3, CollectionEpoch: ptr(epochOne),
		Enabled: false, Bindings: []nodeprov.HistoryBinding{}})
	received, off := receiveApplied()
	// The reset must already have happened, not eventually happen after applied.
	select {
	case <-resetDone:
	default:
		t.Fatal("applied OFF reached collector before map reset")
	}
	if off.Enabled || off.ConfigRevision != 3 || len(flows) != 0 || o.Hello().LastSequence != off.BarrierSequence {
		t.Fatal("socket disable left live destination history")
	}
	var updates, tunnelUpdates, boundaries int
	for _, e := range received {
		switch e.Kind {
		case "flow_update", "tunnel_update":
			if e.ConfigRevision != 2 || e.CollectionEpoch != epochOne || e.Bytes.Final || e.Lifecycle.EndedAt != nil {
				t.Fatal("disable snapshot does not belong to the prior active revision")
			}
			if e.Kind == "tunnel_update" {
				tunnelUpdates++
			}
		case "collection_boundary":
			if e.Boundary.Action == "disabled" {
				boundaries++
				if e.ConfigRevision != 3 || e.Boundary.PriorRevision != 2 || e.Sequence != off.BarrierSequence {
					t.Fatal("disable boundary does not belong to the disabling revision")
				}
			}
		}
		if e.Kind == "flow_end" {
			t.Fatal("socket disable fabricated a flow end")
		}
		if e.Kind == "flow_update" {
			updates++
			if *e.Bytes.Upload != 19 || *e.Bytes.Download != 7 || e.Bytes.Final {
				t.Fatal("socket disable lost cumulative scoped counters")
			}
		}
	}
	if updates != 1 || tunnelUpdates != 1 || boundaries != 1 {
		t.Fatal("missing final nonterminal scoped update")
	}
	// The same UDP association forwards new destinations while collection is OFF.
	// Their history lookup/allocation callback must never run.
	counter := o.flowCounter
	for range 4096 {
		check(t, scope.WithEnabled(func(*CollectionGate) error {
			t.Fatal("OFF destination lookup executed")
			return nil
		}))
	}
	if len(flows) != 0 || len(scope.flows) != 0 || o.flowCounter != counter || tunnel.closed {
		t.Fatal("held OFF association retained destination history")
	}
	send(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 4, CollectionEpoch: ptr(epochTwo),
		Enabled: true, Bindings: []nodeprov.HistoryBinding{}})
	resumed, _ := receiveApplied()
	for _, e := range resumed {
		if e.FlowID != nil {
			t.Fatal("enable recreated an OFF destination journal")
		}
	}
	var next *Flow
	check(t, scope.WithEnabled(func(g *CollectionGate) error {
		if !g.PartialStart() {
			t.Fatal("held association lost partial-start status")
		}
		var err error
		next, err = g.NewFlow(scopedInfo(tunnel, "new-on.test"))
		if err != nil {
			return err
		}
		flows["new-on.test"] = next
		if err := g.SetDestination(next, ptr("effective-on.test"), &nodeprov.HistoryPeer{IP: "192.0.2.2", Port: 443}); err != nil {
			return err
		}
		return g.Observe(next, 2, 3)
	}))
	check(t, scope.Close(normalEnd))
	check(t, tunnel.Close(normalEnd))
	// Reconfiguration is an ordered socket fence after the real closures.
	send(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 5, CollectionEpoch: ptr(epochTwo),
		Enabled: true, Bindings: []nodeprov.HistoryBinding{}})
	final, _ := receiveApplied()
	var starts, ends int
	for _, e := range final {
		if e.FlowID == nil || *e.FlowID != next.ID() {
			continue
		}
		if e.Kind == "flow_start" {
			starts++
			if !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil || *e.Bytes.Upload != 0 || *e.Bytes.Download != 0 {
				t.Fatal("socket reenable lacks partial zero baseline")
			}
		}
		if e.Kind == "flow_end" {
			ends++
			if !e.Bytes.Final || *e.Bytes.Upload != 2 || *e.Bytes.Download != 3 ||
				*e.Destination.RequestedHost != "new-on.test" || *e.Destination.EffectiveHost != "effective-on.test" {
				t.Fatal("socket final counters/destination include another epoch")
			}
		}
	}
	if starts != 1 || ends != 1 || len(flows) != 0 {
		t.Fatal("socket scoped generation lifecycle")
	}
	t.Log("SCOPE_SOCKET_PASS off_new_destinations=4096 retained=0 partial_zero_baseline=true final=2/3 reset_before_applied=true")
	t.Log("DISABLE_REVISION_SOCKET_PASS flow_update=2 tunnel_update=2 disabled_boundary=3 applied=3")
}
