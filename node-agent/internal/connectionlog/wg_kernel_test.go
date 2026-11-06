//go:build linux

package connectionlog

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestWGKernelLive(t *testing.T) {
	if os.Getenv("WG_KERNEL_PROOF") != "1" {
		t.Skip("requires owned kernel namespaces")
	}
	// Testing's context is canceled before cleanup; helpers must instead exit
	// on stdin EOF and be joined before this enclosing lifetime is canceled.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	w, err := NewWireGuardObserver(WireGuardOptions{Interface: "wgW", RuntimeBuild: "lane-W-kernel-proof"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	applied, err := readWGApplied(ctx, "wgW")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PublishAppliedMap(ctx, applied); err != nil {
		t.Fatal(err)
	}
	events := wgSubscribe(t, ctx, w)
	epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	bindings := []nodeprov.HistoryBinding{}
	for i, key := range []string{os.Getenv("WG_KEY1"), os.Getenv("WG_KEY2")} {
		ref := nodeprov.HistoryUUID("33333333-3333-4333-8333-333333333333")
		if i == 1 {
			ref = "44444444-4444-4444-8444-444444444444"
		}
		bindings = append(bindings, nodeprov.HistoryBinding{Runtime: "wireguard", InboundTag: "wgW", LabelKind: "wg_public_key", Label: key, CredentialRef: ref, Kind: "device", BindingRevision: 1})
	}
	control := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: bindings}
	if _, err := events.apply(t, control); err != nil {
		t.Fatal(err)
	}
	boot := w.Hello().ObserverBootID
	tcp := wgEcho(t, "tcp", "10.88.0.1:0")
	udp := wgEcho(t, "udp", "10.88.0.1:0")
	udp6 := wgEcho(t, "udp", "[fd88::1]:0")
	p1 := wgStartClient(t, ctx, os.Getenv("WG_NS1"))
	p2 := wgStartClient(t, ctx, os.Getenv("WG_NS2"))
	start := func(ref nodeprov.HistoryUUID, transport string) nodeprov.HistoryEvent {
		return events.wait(t, func(e nodeprov.HistoryEvent) bool {
			return e.Kind == "flow_start" && e.Destination.Transport == transport && e.Auth.CredentialRef != nil && *e.Auth.CredentialRef == ref
		})
	}
	end := func(reason string) nodeprov.HistoryEvent {
		return events.wait(t, func(e nodeprov.HistoryEvent) bool {
			return e.Kind == "flow_end" && e.Lifecycle.EndReason != nil && *e.Lifecycle.EndReason == reason
		})
	}

	// Two authenticated keys use the same outer loopback IP and destination.
	p1.do(t, wgKernelRequest{Action: "open", Address: tcp})
	one := start(bindings[0].CredentialRef, "tcp")
	p2.do(t, wgKernelRequest{Action: "open", Address: tcp})
	two := start(bindings[1].CredentialRef, "tcp")
	if *one.TunnelID == *two.TunnelID || *one.FlowID == *two.FlowID {
		t.Fatal("same-outer-IP attribution collided")
	}
	p1.do(t, wgKernelRequest{Action: "write", Payload: "held-before-toggle"})
	p2.do(t, wgKernelRequest{Action: "write", Payload: "second-peer-short"})
	p2.do(t, wgKernelRequest{Action: "rst"})
	rst := end("rst")
	if rst.Bytes.Layer != "inner_ip_observed" || *rst.Bytes.Upload < 40 || *rst.Bytes.Download < 40 || rst.Quality.End != "observed" {
		t.Fatalf("wrong reset counters %+v", rst)
	}
	outer := wgKernelCommand(t, ctx, "wg", "show", "wgW", "endpoints")
	for _, line := range strings.Split(strings.TrimSpace(outer), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "127.0.0.1:") {
			t.Fatalf("not same outer IP: %s", outer)
		}
	}
	t.Logf("SAME_OUTER_IP %s", outer)

	// Held TCP remains useful through OFF and a fresh ON epoch, without WG restart.
	control.Enabled = false
	control.ConfigRevision++
	off, err := events.apply(t, control)
	if err != nil {
		t.Fatal(err)
	}
	events.wait(t, func(e nodeprov.HistoryEvent) bool { return e.Sequence == off.BarrierSequence })
	p1.do(t, wgKernelRequest{Action: "write", Payload: "vpn-payload-while-off"})
	if w.Hello().LastSequence != off.BarrierSequence || w.Stats().Capturing {
		t.Fatal("post-disable event or capture")
	}
	epoch2 := nodeprov.HistoryUUID("55555555-5555-4555-8555-555555555555")
	control.Enabled = true
	control.ConfigRevision++
	control.CollectionEpoch = &epoch2
	if _, err := events.apply(t, control); err != nil {
		t.Fatal(err)
	}
	p1.do(t, wgKernelRequest{Action: "write", Payload: "same-tcp-after-enable"})
	resumed := start(bindings[0].CredentialRef, "tcp")
	if resumed.CollectionEpoch != epoch2 || resumed.Lifecycle.StartedAt != nil || *resumed.Bytes.Upload != 0 || *resumed.FlowID == *one.FlowID {
		t.Fatal("blind tuple continuity was guessed")
	}
	p1.do(t, wgKernelRequest{Action: "fin"})
	end("fin")
	if w.Hello().ObserverBootID != boot {
		t.Fatal("toggle replaced observer")
	}
	t.Log("HOT_TOGGLE existing TCP echo survived OFF/ON; observer boot and interface retained")

	// Five complete short TCP windows must each yield a terminal observation.
	for i := 0; i < 5; i++ {
		p2.do(t, wgKernelRequest{Action: "open", Address: tcp})
		start(bindings[1].CredentialRef, "tcp")
		p2.do(t, wgKernelRequest{Action: "write", Payload: fmt.Sprintf("short-%d", i)})
		p2.do(t, wgKernelRequest{Action: "fin"})
		end("fin")
	}
	t.Log("SHORT_FLOWS 5/5 observed FIN closures")
	p1.do(t, wgKernelRequest{Action: "udp", Address: udp, Payload: "peer-one-udp"})
	measuredUDP := start(bindings[0].CredentialRef, "udp")
	p2.do(t, wgKernelRequest{Action: "udp", Address: udp, Payload: "peer-two-udp"})
	start(bindings[1].CredentialRef, "udp")
	p1.do(t, wgKernelRequest{Action: "udp", Address: udp6, Payload: "peer-one-ipv6"})
	v6 := start(bindings[0].CredentialRef, "udp")
	if *v6.Destination.ObservedIP != "fd88::1" {
		t.Fatal("IPv6 destination mismatch")
	}

	// A legitimate endpoint roam changes the outer UDP port, not peer identity.
	wgKernelCommand(t, ctx, "ip", "netns", "exec", os.Getenv("WG_NS1"), "wg", "set", "wgP", "listen-port", "39191")
	p1.do(t, wgKernelRequest{Action: "udp", Address: udp, Payload: "after-roam"})
	roam := start(bindings[0].CredentialRef, "udp")
	if *roam.TunnelID != *resumed.TunnelID {
		t.Fatal("roaming created an exact transport")
	}
	t.Log("ROAM peer binding and activity parent retained")

	// Raw IPv4 fragments traverse real WireGuard encryption and AF_PACKET.
	a, b := wgFragments()
	_, portText, err := net.SplitHostPort(udp)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint16(a[22:24], uint16(port))
	binary.BigEndian.PutUint16(a[4:6], 1001)
	binary.BigEndian.PutUint16(b[4:6], 1001)
	p1.do(t, wgKernelRequest{Action: "raw", Packets: [][]byte{a, b}})
	start(bindings[0].CredentialRef, "udp")
	// A later RST event is a FIFO capture fence for the earlier fragments.
	p2.do(t, wgKernelRequest{Action: "open", Address: tcp})
	p2.do(t, wgKernelRequest{Action: "rst"})
	end("rst")
	w.mu.Lock()
	var fragmentID string
	for key, f := range w.flows {
		if key.local.Port() == 40000 {
			fragmentID = f.handle.ID()
		}
	}
	w.mu.Unlock()
	if fragmentID == "" {
		t.Fatal("kernel fragments produced no attributed flow")
	}
	t.Log("KERNEL_FRAGMENTS first/tail attributed without payload reassembly")

	// Freeze precedes all actual map changes. No desired-map publication.
	if err := w.FreezeAppliedMap(ctx); err != nil {
		t.Fatal(err)
	}
	events.wait(t, func(e nodeprov.HistoryEvent) bool { return e.Gap != nil && e.Gap.Reason == "identity_map_changed" })
	fragmentEnd := events.wait(t, func(e nodeprov.HistoryEvent) bool {
		return e.Kind == "flow_end" && e.FlowID != nil && *e.FlowID == fragmentID
	})
	if *fragmentEnd.Bytes.Upload != 64 {
		t.Fatalf("fragment IP bytes %s", fragmentEnd.Bytes.Upload.String())
	}
	for _, e := range wgEvents(t, w) {
		if e.Kind == "flow_end" && e.FlowID != nil && *e.FlowID == *measuredUDP.FlowID {
			if *e.Bytes.Upload != 40 || *e.Bytes.Download != 40 {
				t.Fatalf("UDP inner-IP counters: %+v", e.Bytes)
			}
			t.Log("EXACT_UDP_BYTES upload=40 download=40 (12 payload + 28 headers)")
		}
	}
	wgKernelCommand(t, ctx, "wg", "set", "wgW", "peer", os.Getenv("WG_KEY1"), "allowed-ips", "10.88.0.4/32,fd88::4/128")
	wgKernelCommand(t, ctx, "ip", "-n", os.Getenv("WG_NS1"), "addr", "del", "10.88.0.2/24", "dev", "wgP")
	wgKernelCommand(t, ctx, "ip", "-n", os.Getenv("WG_NS1"), "addr", "add", "10.88.0.4/24", "dev", "wgP")
	applied, err = readWGApplied(ctx, "wgW")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PublishAppliedMap(ctx, applied); err != nil {
		t.Fatal(err)
	}
	p1.do(t, wgKernelRequest{Action: "udp", Address: udp, Payload: "new-applied-host"})
	changed := start(bindings[0].CredentialRef, "udp")
	if *changed.TunnelID == *roam.TunnelID || w.Stats().MapGeneration != 2 {
		t.Fatal("map generation reused parent")
	}
	t.Log("MAP_CHANGE freeze/gap/actual-publication/new-parent verified")

	// Expiry is driven by the exact state clock, never wall-clock sleeping.
	w.mu.Lock()
	err = w.expire(w.now().Add(wgPeerIdle))
	w.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	end("idle_timeout")
	if err := w.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	stats := w.Stats()
	if stats.KernelPackets == 0 || stats.Packets == 0 || stats.KernelDrops != 0 {
		t.Fatalf("capture statistics %+v", stats)
	}
	t.Logf("KERNEL_STATS %+v", stats)
	// Hold only the observer gate, not forwarding, and exceed its receive queue.
	// A successful echo on a separate socket is the delivery fence, not a sleep.
	w.mu.Lock()
	p1.do(t, wgKernelRequest{Action: "flood", Address: udp})
	p1.do(t, wgKernelRequest{Action: "udp", Address: udp, Payload: "flood-fence"})
	err = w.captureStats()
	w.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if w.Stats().KernelDrops == 0 {
		t.Fatal("kernel overflow was not exercised")
	}
	events.wait(t, func(e nodeprov.HistoryEvent) bool { return e.Gap != nil && e.Gap.Reason == "capture_drop" })
	t.Logf("KERNEL_DROP_PROOF %+v", w.Stats())
	iface, err := net.InterfaceByName("wgW")
	if err != nil || iface.Index != applied.InterfaceIndex {
		t.Fatal("interface restarted")
	}
	control.Enabled = false
	control.ConfigRevision++
	final, err := events.apply(t, control)
	if err != nil {
		t.Fatal(err)
	}
	events.wait(t, func(e nodeprov.HistoryEvent) bool { return e.Sequence == final.BarrierSequence })
	t.Log("WG_KERNEL_PROOF_PASSED")
}
