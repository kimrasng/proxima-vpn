package historytests

import "testing"

func TestXUDPHeldAssociationDropsOffDestinations(t *testing.T) {
	// Given a retained association authenticated before the first enable.
	one, two, ingress := echoUDP(t), echoUDP(t), freePort(t)
	socket := socketPath(t)
	startRuntime(t, config("vless", ingress, 0, false), socket)
	c := connectCollector(t, socket)
	carrier := rawMux(t, ingress)
	muxDatagram(t, carrier, 1, one, 11, true)
	muxDatagram(t, carrier, 2, two, 7, false)
	first := c.apply(t, 1, true, epoch, "vless")
	for _, e := range first {
		if e.FlowID != nil {
			t.Fatal("enable reconstructed an OFF-only UDP destination")
		}
	}
	muxDatagram(t, carrier, 2, one, 5, false)
	firstSamples := c.apply(t, 2, false, epoch, "vless")
	ids := make(map[string]bool)
	for _, e := range firstSamples {
		if e.FlowID != nil {
			ids[*e.FlowID] = true
		}
	}
	if len(ids) == 0 {
		t.Fatal("enabled traffic produced no observation")
	}
	// When the same live association forwards new OFF destinations and resumes.
	muxDatagram(t, carrier, 2, two, 19, false)
	nextEpoch := "44444444-4444-4444-8444-444444444444"
	baseline := c.apply(t, 3, true, nextEpoch, "vless")
	for _, e := range baseline {
		if e.FlowID != nil {
			t.Fatal("reenable retained a retired destination")
		}
	}
	muxDatagram(t, carrier, 2, one, 3, false)
	samples := c.apply(t, 4, false, nextEpoch, "vless")
	// Then only fresh ON bytes appear, with new partial IDs and no guessed reply.
	var upload, download uint64
	for _, e := range samples {
		if e.FlowID == nil {
			continue
		}
		if ids[*e.FlowID] || e.Kind == "flow_end" {
			t.Fatal("toggle reused a retired ID or fabricated an end")
		}
		if !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil {
			t.Fatal("held association claimed an exact flow start")
		}
		if e.Kind == "flow_update" {
			upload += uint64(*e.Bytes.Upload)
			download += uint64(*e.Bytes.Download)
			if *e.Bytes.Download > 0 && e.Destination.RequestedHost != nil {
				t.Fatal("reply guessed a pre-enable request mapping")
			}
		}
	}
	if upload != 3 || download != 3 {
		t.Fatalf("new epoch bytes=%d/%d, want 3/3", upload, download)
	}
}
