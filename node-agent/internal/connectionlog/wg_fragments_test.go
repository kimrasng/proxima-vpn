package connectionlog

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func wgFragments() ([]byte, []byte) {
	a := append(wgTestPacket(17, 0), make([]byte, 8)...)
	binary.BigEndian.PutUint16(a[2:4], uint16(len(a)))
	binary.BigEndian.PutUint16(a[24:26], 24)
	a[6] = 0x20
	b := append([]byte(nil), a[:28]...)
	b[6], b[7] = 0, 2
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	return a, b
}

func TestWGFragmentsAssociateOnlyKnownFirst(t *testing.T) {
	// Given a first IPv4 UDP fragment and its non-first tail.
	w, now := wgTestObserver(t)
	a, b := wgFragments()
	// When the complete packet is observed and the window expires.
	wgFeed(t, w, a, false)
	wgFeed(t, w, b, false)
	*now = now.Add(wgPeerIdle)
	if err := w.expire(*now); err != nil {
		t.Fatal(err)
	}
	// Then both IP lengths, including both IP headers, belong to one flow.
	for _, e := range wgEvents(t, w) {
		if e.Kind == "flow_end" {
			if *e.Bytes.Upload != 64 || !e.Bytes.Final {
				t.Fatalf("%+v", e)
			}
			return
		}
	}
	t.Fatal("missing end")
}

func TestWGFragmentsRejectOutOfOrderOverlapAndExpiry(t *testing.T) {
	for _, mode := range []string{"out of order", "overlap", "expired", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			// Given a bounded fragment cache.
			w, now := wgTestObserver(t)
			a, b := wgFragments()
			// When ambiguity or loss prevents unique association.
			switch mode {
			case "out of order":
				wgFeed(t, w, b, false)
			case "overlap":
				wgFeed(t, w, a, false)
				b[7] = 1
				wgFeed(t, w, b, false)
			case "expired":
				wgFeed(t, w, a, false)
				*now = now.Add(wgFragmentTTL)
				wgFeed(t, w, b, false)
			case "capacity":
				for i := 0; i < wgMaxFragments; i++ {
					w.fragments[wgFragmentKey{id: uint32(i), peer: "occupied"}] = &wgFragment{expires: now.Add(wgFragmentTTL)}
				}
				wgFeed(t, w, a, false)
			}
			// Then the supplier exposes a gap instead of inventing ports.
			found := false
			for _, e := range wgEvents(t, w) {
				if e.Gap != nil && e.Gap.Reason == "unsupported_packet" {
					found = true
				}
			}
			if !found || len(w.fragments) > wgMaxFragments {
				t.Fatal("missing bounded loss")
			}
		})
	}
}

func TestWGIPv6FragmentsPreserveTransportAfterExtensions(t *testing.T) {
	// Given a fragment header whose next header is destination options.
	a, b := wgFragments()
	first := wgTestIPv6(a, 44)
	first[40], first[43] = 60, 1
	first = append(first[:48], append([]byte{17, 0, 0, 0, 0, 0, 0, 0}, first[48:]...)...)
	binary.BigEndian.PutUint16(first[4:6], uint16(len(first)-40))
	tail := wgTestIPv6(b, 44)
	tail[40] = 60
	binary.BigEndian.PutUint16(tail[42:44], 24)
	w, _ := wgTestObserver(t)
	w.hosts[netip.MustParseAddr("fd88::2")] = wgKey(1)
	// When both fragments are observed.
	wgFeed(t, w, first, false)
	wgFeed(t, w, tail, false)
	// Then the first fragment's resolved UDP ports survive the extension chain.
	if len(w.flows) != 1 || len(w.fragments) != 0 || w.stats.Unsupported != 0 {
		t.Fatalf("%+v", w.Stats())
	}
}

func TestWGAppliedMapRejectsAmbiguity(t *testing.T) {
	host := netip.MustParsePrefix("10.88.0.2/32")
	tests := [][]WireGuardPeer{
		{{PublicKey: wgKey(1), AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.88.0.0/24")}}},
		{{PublicKey: wgKey(1), AllowedIPs: []netip.Prefix{host}}, {PublicKey: wgKey(2), AllowedIPs: []netip.Prefix{host}}},
		{{PublicKey: "not a key", AllowedIPs: []netip.Prefix{host}}},
	}
	for _, peers := range tests {
		// Given / When / Then
		if _, err := wgHostMap(peers); err == nil {
			t.Fatal("accepted ambiguous map")
		}
	}
}

func TestWGTruncationDegradesFinality(t *testing.T) {
	// Given a live measured window.
	w, now := wgTestObserver(t)
	wgFeed(t, w, wgTestPacket(17, 0), false)
	// When a truncated capture is followed by expiry.
	if err := w.consume(wgReceived{truncated: true}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(wgPeerIdle)
	if err := w.expire(*now); err != nil {
		t.Fatal(err)
	}
	// Then known samples survive but final totals are not claimed.
	for _, e := range wgEvents(t, w) {
		if e.Kind == "flow_end" && (e.Bytes.Final || *e.Bytes.Upload != 28 || e.Quality.Completeness != "unknown") {
			t.Fatalf("%+v", e)
		}
	}
}
