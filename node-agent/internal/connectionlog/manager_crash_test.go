package connectionlog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
)

// This test binary is also the disposable real agent subprocess for SIGKILL QA.
func TestManager_CrashHelper(t *testing.T) {
	if os.Getenv("HISTORY_CRASH_HELPER") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	api := client.NewAPIClient(&config.AgentConfig{ServerURL: os.Getenv("HISTORY_SERVER"),
		NodeID: "node-a", APIKey: "history-key"})
	m := newManager(t, api, os.Getenv("HISTORY_DIRECTORY"))
	must(t, m.Register(NewUnixSource("xray", os.Getenv("HISTORY_SOCKET"))))
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	go func() {
		input := bufio.NewScanner(os.Stdin)
		for input.Scan() {
			if input.Text() == "upload" {
				err := m.UploadOnce(ctx)
				fmt.Printf("UPLOAD %v\n", err)
			}
		}
	}()
	var ready bool
	var last string
	for {
		changed := m.Changes()
		s := m.Status()
		if s.State == "collecting" && !ready {
			fmt.Println("READY")
			ready = true
		}
		if len(s.Producers) == 1 {
			current := s.Producers[0].LastDurableSequence.String()
			if current != last {
				fmt.Printf("DURABLE %s\n", current)
				last = current
			}
		}
		select {
		case <-changed:
		case err := <-done:
			t.Fatalf("helper Run stopped: %v", err)
		case <-ctx.Done():
			t.Fatal("helper timed out")
		}
	}
}

func TestPipeline_SIGKILLAgentResumesSocketAndReplaysFrozenHTTP(t *testing.T) {
	// Given an actual agent subprocess connected to a surviving exporter and HTTP API.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wire, api := newWireAPI(t)
	obs, socket := observer(t, ctx, "xray")
	directory := t.TempDir()
	executable, err := os.Executable()
	must(t, err)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestManager_CrashHelper$")
	// The fixture owns the URL; the production client's credential fields stay private.
	serverURL := wire.serverURL
	command.Env = append(os.Environ(), "HISTORY_CRASH_HELPER=1", "HISTORY_SERVER="+serverURL,
		"HISTORY_DIRECTORY="+directory, "HISTORY_SOCKET="+socket)
	input, err := command.StdinPipe()
	must(t, err)
	stdout, err := command.StdoutPipe()
	must(t, err)
	command.Stderr = command.Stdout
	lines := make(chan string, 32)
	scannerDone := make(chan struct{})
	go func() {
		defer close(scannerDone)
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	must(t, command.Start())
	joined := false
	defer func() {
		input.Close()
		if !joined {
			command.Process.Kill()
			command.Wait()
		}
		<-scannerDone
	}()
	receive := func(prefix string) {
		t.Helper()
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("helper closed before %q", prefix)
				}
				if strings.HasPrefix(line, prefix) {
					t.Logf("agent subprocess: %s", line)
					return
				}
			case <-ctx.Done():
				t.Fatalf("helper did not emit %q", prefix)
			}
		}
	}
	receive("READY")
	tunnel, err := obs.NewTunnel(connectionhistory.TunnelInfo{Protocol: "vless",
		InboundTag: "a", LabelKind: "xray_email", Label: "authenticated"})
	must(t, err)
	must(t, tunnel.Close(connectionhistory.End{Reason: "eof", Semantics: "local_owner_closed"}))
	receive("DURABLE " + obs.Hello().LastSequence.String())
	wire.mu.Lock()
	wire.fail = true
	wire.mu.Unlock()
	fmt.Fprintln(input, "upload")
	receive("UPLOAD history request status 503")
	boot, sequence := obs.BootID(), obs.Hello().LastSequence

	// When SIGKILL terminates the agent after its first HTTP request lost a receipt.
	must(t, command.Process.Kill())
	if err := command.Wait(); err == nil {
		t.Fatal("agent subprocess did not crash")
	}
	joined = true
	wire.mu.Lock()
	wire.fail = false
	first := append([]byte(nil), wire.bodies[len(wire.bodies)-1]...)
	wire.mu.Unlock()
	m := newManager(t, api, directory)
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(NewUnixSource("xray", socket)))
	must(t, m.Poll(ctx))
	stop := startSource(t, ctx, m, "xray")
	defer stop()
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" })
	must(t, m.UploadOnce(ctx))
	// Then the disk checkpoint resumes the same runtime and sends identical HTTP bytes.
	wire.mu.Lock()
	replayed := wire.bodies[len(wire.bodies)-1]
	if !bytes.Equal(first, replayed) {
		t.Error("SIGKILL recovery rebuilt immutable batch")
	}
	wire.mu.Unlock()
	if obs.BootID() != boot || obs.Hello().LastSequence != sequence {
		t.Fatal("agent crash recovery re-enabled or restarted runtime")
	}
	must(t, m.UploadOnce(ctx))
	wire.mu.Lock()
	var recovery client.ConnectionHistoryBatch
	must(t, json.Unmarshal(wire.bodies[len(wire.bodies)-1], &recovery))
	wire.mu.Unlock()
	found := false
	for _, event := range recovery.Events {
		if event.Gap != nil && event.Gap.ObserverBootID == boot && event.Gap.Reason == "producer_lost" {
			found = true
		}
		if (event.Kind == "flow_end" || event.Kind == "tunnel_end") &&
			(event.ObserverBootID != boot || event.Sequence > sequence) {
			t.Fatal("agent recovery invented an owner end")
		}
	}
	if !found {
		t.Fatal("SIGKILL omitted unknown interval since last durable checkpoint")
	}
}
