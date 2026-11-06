package connectionlog

import (
	"encoding/base64"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func wgKey(n byte) string {
	b := make([]byte, 32)
	b[0] = n
	return base64.StdEncoding.EncodeToString(b)
}

func wgTestObserver(t *testing.T) (*WireGuardObserver, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	w, err := NewWireGuardObserver(WireGuardOptions{Interface: "wgtest", RuntimeBuild: "test"})
	if err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return now }
	w.history, err = connectionhistory.NewObserver(connectionhistory.Options{ProducerID: "wireguard:wgtest", Runtime: "wireguard", RuntimeBuild: "test", Clock: w.now})
	if err != nil {
		t.Fatal(err)
	}
	w.hosts = map[netip.Addr]string{netip.MustParseAddr("10.88.0.2"): wgKey(1), netip.MustParseAddr("10.88.0.3"): wgKey(2)}
	w.enabled, w.frozen = true, false
	w.stats.MapGeneration = 1
	epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	bindings := []nodeprov.HistoryBinding{
		{Runtime: "wireguard", InboundTag: "wgtest", LabelKind: "wg_public_key", Label: wgKey(1), CredentialRef: "33333333-3333-4333-8333-333333333333", Kind: "device", BindingRevision: 1},
		{Runtime: "wireguard", InboundTag: "wgtest", LabelKind: "wg_public_key", Label: wgKey(2), CredentialRef: "44444444-4444-4444-8444-444444444444", Kind: "device", BindingRevision: 1},
	}
	if _, err := w.history.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: bindings}); err != nil {
		t.Fatal(err)
	}
	return w, &now
}

func wgEvents(t *testing.T, w *WireGuardObserver) []nodeprov.HistoryEvent {
	t.Helper()
	lines, err := w.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: w.Hello().ObserverBootID})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]nodeprov.HistoryEvent, 0, len(lines))
	for _, line := range lines {
		e, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	return events
}

func wgFeed(t *testing.T, w *WireGuardObserver, b []byte, out bool) {
	t.Helper()
	if err := w.consume(wgReceived{data: b, outgoing: out}); err != nil {
		t.Fatal(err)
	}
}

func wgReply(b []byte) []byte {
	c := append([]byte(nil), b...)
	copy(c[12:16], b[16:20])
	copy(c[16:20], b[12:16])
	copy(c[20:22], b[22:24])
	copy(c[22:24], b[20:22])
	return c
}

func TestWGDirectionIdentityAndRST(t *testing.T) {
	// Given two applied keys with distinct inner IPs (outer IP is irrelevant).
	w, _ := wgTestObserver(t)
	// When both keys use the same destination and one flow resets.
	a, b := wgTestPacket(6, 2), wgTestPacket(17, 0)
	b[15] = 3
	wgFeed(t, w, a, false)
	wgFeed(t, w, b, false)
	r := wgReply(a)
	r[33] = 4
	wgFeed(t, w, r, true)
	// Then keys/parents are distinct and both directions count full IP lengths.
	var parents []string
	var ended *nodeprov.HistoryEvent
	for _, e := range wgEvents(t, w) {
		if e.Kind == "flow_start" {
			parents = append(parents, *e.TunnelID)
		}
		if e.Kind == "flow_end" {
			copy := e
			ended = &copy
		}
	}
	if len(parents) != 2 || parents[0] == parents[1] {
		t.Fatalf("parents %v", parents)
	}
	if ended == nil || *ended.Bytes.Upload != 40 || *ended.Bytes.Download != 40 || ended.Bytes.Layer != "inner_ip_observed" || !ended.Bytes.Final {
		t.Fatalf("end %+v", ended)
	}
	if *ended.Auth.CredentialRef != "33333333-3333-4333-8333-333333333333" || *ended.Lifecycle.EndReason != "rst" || ended.Lifecycle.StartedAt != nil || ended.Quality.End != "observed" {
		t.Fatalf("semantics %+v", ended)
	}
	if ended.Destination.RequestedHost != nil || *ended.Destination.ObservedIP != "10.88.0.1" {
		t.Fatal("invented destination")
	}
}

func TestWGSYNGenerationAndFIN(t *testing.T) {
	// Given a live tuple with a known SYN sequence.
	w, _ := wgTestObserver(t)
	b := wgTestPacket(6, 2)
	wgFeed(t, w, b, false)
	// When SYN retransmits, then a new sequence replaces it and both FINs arrive.
	wgFeed(t, w, b, false)
	binary.BigEndian.PutUint32(b[24:28], 99)
	wgFeed(t, w, b, false)
	b[33] = 0x11
	wgFeed(t, w, b, false)
	wgFeed(t, w, wgReply(b), true)
	// Then retransmission retains the generation and two distinct ends are observed.
	var starts, ends int
	reasons := map[string]bool{}
	for _, e := range wgEvents(t, w) {
		if e.Kind == "flow_start" {
			starts++
		}
		if e.Kind == "flow_end" {
			ends++
			reasons[*e.Lifecycle.EndReason] = true
		}
	}
	if starts != 2 || ends != 2 || !reasons["tuple_reused"] || !reasons["fin"] {
		t.Fatalf("starts=%d ends=%d reasons=%v", starts, ends, reasons)
	}
}

func TestWGPeerExpiryUsesLastTraffic(t *testing.T) {
	// Given an observed flow and an injected monotonic clock.
	w, now := wgTestObserver(t)
	wgFeed(t, w, wgTestPacket(17, 0), false)
	last := *now
	// When the peer reaches its sixty-second idle boundary.
	*now = now.Add(wgPeerIdle)
	if err := w.expire(*now); err != nil {
		t.Fatal(err)
	}
	// Then children close before parent, with distinct activity and closure times.
	es := wgEvents(t, w)
	end := es[len(es)-1]
	if end.Kind != "tunnel_end" || *end.Lifecycle.LastObservedAt != nodeprov.HistoryTimestamp(last) || *end.Lifecycle.EndedAt != nodeprov.HistoryTimestamp(*now) || *end.Lifecycle.EndSemantics != "activity_expired" {
		t.Fatalf("%+v", end)
	}
	if es[len(es)-2].Kind != "flow_end" || len(w.flows) != 0 {
		t.Fatal("child not expired")
	}
}

func TestWGUnknownPeerAndMapFreeze(t *testing.T) {
	// Given a mapped live flow.
	w, _ := wgTestObserver(t)
	wgFeed(t, w, wgTestPacket(17, 0), false)
	// When an unknown source arrives and attribution freezes before reassignment.
	b := wgTestPacket(17, 0)
	b[15] = 99
	wgFeed(t, w, b, false)
	if err := w.FreezeAppliedMap(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then no invented peer exists and previous finals are explicitly incomplete.
	if !w.frozen || len(w.flows) != 0 || w.stats.UnknownPeer != 1 {
		t.Fatal(w.Stats())
	}
	for _, e := range wgEvents(t, w) {
		if e.Kind == "flow_end" && (e.Bytes.Final || e.Quality.Completeness != "unknown") {
			t.Fatalf("false final %+v", e)
		}
	}
}
