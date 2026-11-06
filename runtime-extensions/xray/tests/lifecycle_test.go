package historytests

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func transfer(t *testing.T, conn net.Conn, size int) {
	t.Helper()
	payload := bytes.Repeat([]byte{byte(size)}, size)
	written := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		written <- err
	}()
	got := make([]byte, size)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestHeldFlowHotToggle(t *testing.T) {
	// Given an actual authenticated tunnel and held flow while collection is OFF.
	target, ingress, client := echoTCP(t), freePort(t), freePort(t)
	socket := socketPath(t)
	runtime := startRuntime(t, config("vless", ingress, 0, false), socket)
	c := connectCollector(t, socket)
	startRuntime(t, config("vless", ingress, client, true), "")
	conn := socksConnect(t, client, target)
	transfer(t, conn, 17)
	pid, boot := runtime.cmd.Process.Pid, c.boot

	// When ON/OFF/ON changes collection without restarting the real flow.
	first := c.apply(t, 1, true, epoch, "vless")
	var flowID, tunnelID string
	for _, e := range first {
		if e.Kind == "flow_start" {
			flowID, tunnelID = *e.FlowID, *e.TunnelID
			if !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil || *e.Bytes.Upload != 0 || *e.Bytes.Download != 0 {
				t.Fatal("enable did not establish zero partial baseline")
			}
		}
	}
	if flowID == "" {
		t.Fatal("enable omitted the already-open flow")
	}
	transfer(t, conn, 23)
	disabled := c.apply(t, 2, false, epoch, "vless")
	found := false
	for _, e := range disabled {
		if e.Kind == "flow_end" || e.Kind == "tunnel_end" {
			t.Fatal("disable invented end")
		}
		if e.Kind == "flow_update" && *e.FlowID == flowID {
			found = true
			if e.Bytes.Final || *e.Bytes.Upload != 23 || *e.Bytes.Download != 23 {
				t.Fatalf("bad disable sample: %#v", e.Bytes)
			}
		}
	}
	if !found {
		t.Fatal("disable omitted final nonterminal sample")
	}
	transfer(t, conn, 31)
	nextEpoch := "44444444-4444-4444-8444-444444444444"
	resumed := c.apply(t, 3, true, nextEpoch, "vless")
	for _, e := range resumed {
		if string(e.CollectionEpoch) != nextEpoch {
			t.Fatal("post-disable detailed old-epoch event")
		}
		if e.Kind == "flow_start" && (*e.FlowID != flowID || *e.TunnelID != tunnelID || *e.Bytes.Upload != 0 || *e.Bytes.Download != 0) {
			t.Fatal("owner changed or OFF bytes counted")
		}
	}
	transfer(t, conn, 13)
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatal(err)
	}

	// Then only post-enable bytes are final, with the same process and real owner.
	for {
		e, err := nodeprov.DecodeHistoryEvent(c.next(t))
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind != "flow_end" {
			continue
		}
		if *e.FlowID != flowID || *e.TunnelID != tunnelID || *e.Bytes.Upload != 13 || *e.Bytes.Download != 13 || !e.Bytes.Final {
			t.Fatalf("incorrect resumed final: %#v", e)
		}
		break
	}
	if runtime.cmd.Process.Pid != pid || c.boot != boot {
		t.Fatal("toggle restarted runtime")
	}
}

func muxConfig(ingress, client int) string {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(config("vless", ingress, client, true)), &cfg); err != nil {
		panic(err)
	}
	out := cfg["outbounds"].([]any)[0].(map[string]any)
	out["mux"] = map[string]any{"enabled": true, "concurrency": 8, "xudpConcurrency": 8}
	data, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestMuxIndependentShortDestinations(t *testing.T) {
	// Given two simultaneous child streams under one real VLESS mux tunnel.
	first, second, ingress, client := echoTCP(t), echoTCP(t), freePort(t), freePort(t)
	socket := socketPath(t)
	startRuntime(t, config("vless", ingress, 0, false), socket)
	c := connectCollector(t, socket)
	c.apply(t, 1, true, epoch, "vless")
	startRuntime(t, muxConfig(ingress, client), "")
	a, b := socksConnect(t, client, first), socksConnect(t, client, second)
	transfer(t, a, 23)
	transfer(t, b, 29)
	// When both children end while the outer mux runtime remains alive.
	if err := a.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := b.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, a); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, b); err != nil {
		t.Fatal(err)
	}
	// Then each destination has its own exact terminal counters and shared parent.
	ends := make(map[int]nodeprov.HistoryEvent)
	for len(ends) < 2 {
		e, err := nodeprov.DecodeHistoryEvent(c.next(t))
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind != "flow_end" {
			continue
		}
		if e.Destination == nil || e.Destination.RequestedPort == nil {
			t.Fatal("missing mux destination")
		}
		port := *e.Destination.RequestedPort
		if _, exists := ends[port]; exists {
			t.Fatal("duplicate terminal flow")
		}
		ends[port] = e
	}
	x, y := ends[first], ends[second]
	if x.FlowID == nil || y.FlowID == nil || *x.FlowID == *y.FlowID || *x.TunnelID != *y.TunnelID {
		t.Fatal("mux ownership mismatch")
	}
	if *x.Bytes.Upload != 23 || *x.Bytes.Download != 23 || *y.Bytes.Upload != 29 || *y.Bytes.Download != 29 {
		t.Fatal("mux payload counters mismatch")
	}
}
