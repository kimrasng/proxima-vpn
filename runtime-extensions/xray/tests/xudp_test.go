package historytests

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func echoUDP(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 65535)
		for {
			n, addr, err := c.ReadFrom(b)
			if err != nil {
				return
			}
			if _, err := c.WriteTo(b[:n], addr); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Log(err)
		}
		<-done
	})
	return c.LocalAddr().(*net.UDPAddr).Port
}

func rawMux(t *testing.T, ingress int) net.Conn {
	return rawMuxUser(t, ingress, identity)
}

func rawMuxUser(t *testing.T, ingress int, userID string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ingress), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	id, err := hex.DecodeString(strings.ReplaceAll(userID, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	header := append([]byte{0}, id...)
	header = append(header, 0, 3)
	if _, err := c.Write(header); err != nil {
		t.Fatal(err)
	}
	return c
}

func muxDatagram(t *testing.T, c net.Conn, status byte, port, size int, first bool) {
	t.Helper()
	meta := []byte{0, 1, status, 1, 2, byte(port >> 8), byte(port), 1, 127, 0, 0, 1}
	if status == 1 {
		meta = append(meta, 1, 2, 3, 4, 5, 6, 7, 8)
	}
	frame := []byte{byte(len(meta) >> 8), byte(len(meta))}
	frame = append(frame, meta...)
	frame = append(frame, byte(size>>8), byte(size))
	payload := bytes.Repeat([]byte{byte(size)}, size)
	frame = append(frame, payload...)
	if _, err := c.Write(frame); err != nil {
		t.Fatal(err)
	}
	if first {
		header := make([]byte, 2)
		if _, err := io.ReadFull(c, header); err != nil {
			t.Fatal(err)
		}
		if header[0] != 0 || header[1] != 0 {
			t.Fatalf("VLESS response %x", header)
		}
	}
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(c, lenBuf); err != nil {
		t.Fatal(err)
	}
	responseMeta := make([]byte, int(binary.BigEndian.Uint16(lenBuf)))
	if _, err := io.ReadFull(c, responseMeta); err != nil {
		t.Fatal(err)
	}
	if len(responseMeta) < 4 || responseMeta[2] == 3 || responseMeta[3]&1 == 0 {
		t.Fatalf("unexpected mux response %x", responseMeta)
	}
	if _, err := io.ReadFull(c, lenBuf); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, int(binary.BigEndian.Uint16(lenBuf)))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("XUDP payload mismatch %x", got)
	}
}

func TestXUDPMultipleDestinationsAndReattachment(t *testing.T) {
	// Given one authenticated retained XUDP association, two actual UDP endpoints.
	one, two, ingress := echoUDP(t), echoUDP(t), freePort(t)
	socket := socketPath(t)
	startRuntime(t, config("vless", ingress, 0, false), socket)
	c := connectCollector(t, socket)
	c.apply(t, 1, true, epoch, "vless")
	old := rawMux(t, ingress)
	muxDatagram(t, old, 1, one, 11, true)
	muxDatagram(t, old, 2, two, 7, false)
	// When the same association reattaches to another authenticated carrier.
	next := rawMux(t, ingress)
	muxDatagram(t, next, 1, one, 5, true)
	snapshots := c.apply(t, 2, false, epoch, "vless")
	// Then old segments close with exact old counters, and new bytes belong only
	// to the new parent. Disabling does not fabricate a retained association end.
	oldEnds := make(map[int]nodeprov.HistoryEvent)
	newUpdates := make(map[int]nodeprov.HistoryEvent)
	for _, e := range snapshots {
		if e.Destination == nil || e.Destination.RequestedPort == nil {
			continue
		}
		p := *e.Destination.RequestedPort
		switch e.Kind {
		case "flow_end":
			if e.Lifecycle.EndReason == nil || *e.Lifecycle.EndReason != "parent_changed" {
				t.Fatal("XUDP reattachment invented transport close")
			}
			if e.TunnelID == nil {
				if e.Auth.Kind != "unknown" || e.Quality.Parent != "unknown" {
					t.Fatal("detached segment retained a guessed account or parent")
				}
				continue
			}
			oldEnds[p] = e
		case "flow_update":
			newUpdates[p] = e
		}
	}
	if len(oldEnds) != 2 || len(newUpdates) != 2 {
		t.Fatalf("old destination ends=%d new destination updates=%d", len(oldEnds), len(newUpdates))
	}
	for port, size := range map[int]uint64{one: 11, two: 7} {
		e := oldEnds[port]
		if e.Bytes.Upload == nil || uint64(*e.Bytes.Upload) != size || uint64(*e.Bytes.Download) != size {
			t.Fatalf("wrong old counters: %#v", e.Bytes)
		}
		n := newUpdates[port]
		if n.TunnelID == nil || *n.TunnelID == *e.TunnelID || *n.FlowID == *e.FlowID {
			t.Fatal("retained association reused old observation identity")
		}
	}
	if *newUpdates[one].Bytes.Upload != 5 || *newUpdates[one].Bytes.Download != 5 {
		t.Fatal("new parent missing exact payload")
	}
}

func TestXUDPDetachedCarrierClosesAuthenticatedOwner(t *testing.T) {
	// Given an acknowledged datagram on a retained association.
	target, ingress := echoUDP(t), freePort(t)
	socket := socketPath(t)
	startRuntime(t, config("vless", ingress, 0, false), socket)
	c := connectCollector(t, socket)
	c.apply(t, 1, true, epoch, "vless")
	carrier := rawMux(t, ingress)
	muxDatagram(t, carrier, 1, target, 11, true)
	// Subscribe before closing: a retained UDP socket must not keep the closed
	// authenticated carrier alive in history.
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	if err := carrier.Close(); err != nil {
		t.Fatal(err)
	}
	var tunnel string
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				t.Fatal("runtime lost while awaiting authenticated carrier close")
			}
			t.Logf("history %s", line)
			e, err := nodeprov.DecodeHistoryEvent(line)
			if err != nil {
				t.Fatal(err)
			}
			if e.Kind == "tunnel_start" {
				tunnel = *e.TunnelID
			}
			if e.Kind == "tunnel_end" {
				if e.TunnelID == nil || *e.TunnelID != tunnel {
					t.Fatal("unrelated tunnel end")
				}
				return
			}
		case <-deadline.C:
			t.Fatal("closed XUDP carrier still owns retained destination flows: detach observation segments from the authenticated parent before tunnel finalization")
		}
	}
}
