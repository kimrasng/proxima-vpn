package historytests

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestUDPActivityOwnerRetirement(t *testing.T) {
	for _, protocol := range []string{"shadowsocks", "shadowsocks2022", "shadowsocks2022multi"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			// Given actual authenticated UDP with normal, unmodified idle timers.
			target, ingress, client := echoUDP(t), freePort(t), freePort(t)
			socket := socketPath(t)
			startRuntimeFor(t, udpLifecycleConfig(t, protocol, ingress), socket, 5*time.Minute)
			c := connectCollector(t, socket)
			c.apply(t, 1, true, epoch, protocol)
			startRuntimeFor(t, config(protocol, ingress, client, true), "", 5*time.Minute)
			data, relay := socksUDP(t, client)
			deadline := time.NewTimer(4 * time.Minute)
			defer deadline.Stop()
			// When traffic becomes idle, let the real server retire its association.
			udpLifecyclePacket(t, data, relay, target)
			var owner string
			children := make(map[string]bool)
			for {
				event := udpLifecycleEvent(t, c, deadline.C)
				switch event.Kind {
				case "tunnel_start":
					if owner != "" || event.Lifecycle.Scope != "activity_window" {
						t.Fatal("expected one authenticated activity owner")
					}
					owner = *event.TunnelID
				case "flow_start":
					children[*event.FlowID] = false
				case "flow_end":
					if event.TunnelID == nil || *event.TunnelID != owner ||
						!event.Bytes.Final || *event.Bytes.Upload != 11 || *event.Bytes.Download != 11 {
						t.Fatal("wrong child owner or final counters")
					}
					ended, exists := children[*event.FlowID]
					if !exists || ended {
						t.Fatal("missing start or duplicate child end")
					}
					children[*event.FlowID] = true
				case "tunnel_end":
					// Then the parent closes only after its measured child.
					if *event.TunnelID != owner || len(children) != 1 {
						t.Fatal("wrong retired activity owner")
					}
					for _, ended := range children {
						if !ended {
							t.Fatal("activity parent ended before child")
						}
					}
					if event.Lifecycle.Scope != "activity_window" ||
						*event.Lifecycle.EndSemantics != "observation_segment_closed" ||
						*event.Lifecycle.EndReason != "transport_close" || event.Quality.End != "observed" {
						t.Fatal("activity closure claimed an exact transport disconnect")
					}
					kind, quality := "shared", "unknown"
					if protocol == "shadowsocks2022multi" {
						kind, quality = "device", "exact"
					}
					if event.Auth.Kind != kind || event.Quality.Identity != quality ||
						event.Auth.CredentialRef == nil || string(*event.Auth.CredentialRef) != credential {
						t.Fatal("authenticated attribution changed")
					}
					// Disable forces a live-owner update; enable enumerates every
					// surviving owner. Neither may resurrect the closed association.
					events := c.apply(t, 2, false, epoch, protocol)
					next := "44444444-4444-4444-8444-444444444444"
					events = append(events, c.apply(t, 3, true, next, protocol)...)
					for _, later := range events {
						if later.TunnelID != nil && *later.TunnelID == owner {
							t.Fatalf("retired owner reappeared as %s", later.Kind)
						}
					}
					return
				}
			}
		})
	}
}

func udpLifecycleConfig(t *testing.T, protocol string, ingress int) string {
	t.Helper()
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(config(protocol, ingress, 0, false)), &cfg); err != nil {
		t.Fatal(err)
	}
	// SS2022 honors the existing connection-idle policy. Legacy SS keeps its
	// upstream 120-second worker idle threshold and 60-second sweep unchanged.
	cfg["policy"] = json.RawMessage(`{"levels":{"0":{"connIdle":2,"uplinkOnly":0,"downlinkOnly":0}}}`)
	result, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}

func udpLifecyclePacket(t *testing.T, data *net.UDPConn, relay, target int) {
	t.Helper()
	frame := []byte{0, 0, 0, 1, 127, 0, 0, 1, byte(target >> 8), byte(target)}
	frame = append(frame, bytes.Repeat([]byte{0x47}, 11)...)
	if _, err := data.WriteToUDP(frame, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: relay}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 65535)
	n, _, err := data.ReadFromUDP(reply)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply[:n], frame) {
		t.Fatal("UDP payload changed")
	}
}

func udpLifecycleEvent(t *testing.T, c *collector, deadline <-chan time.Time) nodeprov.HistoryEvent {
	t.Helper()
	select {
	case line, ok := <-c.lines:
		if !ok {
			t.Fatal("history socket closed before retirement")
		}
		t.Logf("history %s", strings.TrimSpace(string(line)))
		event, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			t.Fatal(err)
		}
		return event
	case <-deadline:
		t.Fatal("normal UDP association did not retire within its idle bound")
		return nodeprov.HistoryEvent{}
	}
}
