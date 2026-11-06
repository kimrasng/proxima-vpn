package historytests

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestCapabilityProbe(t *testing.T) {
	// Given the exact executable used for the protocol matrix.
	cmd := exec.CommandContext(t.Context(), os.Getenv("XRAY_HISTORY_BINARY"), "history-capabilities")
	// When the agent's deployment probe executes without starting VPN listeners.
	wire, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	frame, err := nodeprov.DecodeHistoryControl(wire)
	if err != nil {
		t.Fatal(err)
	}
	// Then the control envelope carries the pinned build and required hooks.
	hello, ok := frame.(nodeprov.HistoryHello)
	if !ok || hello.RuntimeBuild != "xray-a4a3dff5-history-v1" || hello.Enabled {
		t.Fatal("wrong probe")
	}
	for _, cap := range []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"} {
		if !slices.Contains(hello.Capabilities, cap) {
			t.Fatalf("missing %s", cap)
		}
	}
}

func TestCrashLeavesEndsAndFinalBytesUnknown(t *testing.T) {
	// Given a live acknowledged flow with the collector subscribed before auth.
	target, ingress, client := echoTCP(t), freePort(t), freePort(t)
	socket := socketPath(t)
	server := startRuntime(t, config("vless", ingress, 0, false), socket)
	c := connectCollector(t, socket)
	c.apply(t, 1, true, epoch, "vless")
	startRuntime(t, config("vless", ingress, client, true), "")
	conn := socksConnect(t, client, target)
	transfer(t, conn, 23)
	for {
		e, err := nodeprov.DecodeHistoryEvent(c.next(t))
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == "flow_start" {
			break
		}
	}
	// When the actual observer process is killed, not gracefully disconnected.
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	server.killed = true
	if err := server.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Then stream loss supplies no fabricated terminal entity or final total.
	// The agent owns the producer_lost gap for this now-absent boot.
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				return
			}
			e, err := nodeprov.DecodeHistoryEvent(line)
			if err != nil {
				t.Fatal(err)
			}
			if e.Kind == "flow_end" || e.Kind == "tunnel_end" || e.Bytes.Final {
				t.Fatal("crash fabricated a precise terminal event")
			}
		case <-deadline.C:
			t.Fatal("crashed exporter stream did not close")
		}
	}
}
