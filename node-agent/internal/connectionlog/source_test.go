package connectionlog

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestSource_WithholdsACKWhenAnyDurabilityBoundaryFails(t *testing.T) {
	for _, boundary := range []string{"record", "directory", "checkpoint"} {
		t.Run(boundary, func(t *testing.T) {
			// Given a real Unix stream exporter and an injected disk failure.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, api := newWireAPI(t)
			m := newManager(t, api, t.TempDir())
			defer func() { must(t, m.Close()) }()
			dir, err := os.MkdirTemp("", "ha-")
			must(t, err)
			defer func() { must(t, os.RemoveAll(dir)) }()
			must(t, os.Chmod(dir, 0o700))
			path := filepath.Join(dir, "s")
			listener, err := net.Listen("unix", path)
			must(t, err)
			defer listener.Close()
			must(t, os.Chmod(path, 0o600))
			must(t, m.Register(NewUnixSource("xray", path)))
			h, event := fixture(t)
			h.LastSequence = 1
			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { conn.Close() })
				defer stop()
				if err := sendControl(conn, h); err != nil {
					serverDone <- err
					return
				}
				reader := bufio.NewReader(conn)
				line, err := reader.ReadBytes('\n')
				if err != nil {
					serverDone <- err
					return
				}
				control, err := nodeprov.DecodeHistoryControl(line)
				if err != nil {
					serverDone <- err
					return
				}
				if _, ok := control.(nodeprov.HistoryResume); !ok {
					serverDone <- errors.New("expected resume")
					return
				}
				m.out.mu.Lock()
				m.out.sync = func(f *os.File) error {
					name := filepath.Base(f.Name())
					if (boundary == "record" && strings.HasSuffix(name, ".jsonl.tmp")) ||
						(boundary == "directory" && name == "records") ||
						(boundary == "checkpoint" && name == "state") {
						return syscall.ENOSPC
					}
					return f.Sync()
				}
				m.out.mu.Unlock()
				data, err := nodeprov.EncodeHistoryEvent(event)
				if err != nil {
					serverDone <- err
					return
				}
				// Fragment every frame independently of receiver read boundaries.
				for len(data) > 0 {
					n := min(7, len(data))
					if _, err := conn.Write(data[:n]); err != nil {
						serverDone <- err
						return
					}
					data = data[n:]
				}
				ack, err := reader.ReadBytes('\n')
				if len(ack) != 0 {
					serverDone <- errors.New("ACK escaped before fsync")
					return
				}
				if !errors.Is(err, io.EOF) {
					serverDone <- err
					return
				}
				serverDone <- nil
			}()
			// When collecting the frame requires a failed durability operation.
			err = m.collect(ctx, m.workers["xray"])
			// Then the real socket receives EOF, not an unsafe ACK.
			if !errors.Is(err, syscall.ENOSPC) {
				t.Fatalf("expected disk failure, got %v", err)
			}
			select {
			case err := <-serverDone:
				must(t, err)
			case <-ctx.Done():
				t.Fatal("socket server did not join")
			}
			m.out.sync = (*os.File).Sync
		})
	}
}

func TestManager_AggregatesAllProducerBarriersAndRetainsDesiredOnPollFailure(t *testing.T) {
	// Given two independent exporters and manifests for both runtimes.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire, api := newWireAPI(t)
	xray, xrayPath := observer(t, ctx, "xray")
	_, hyPath := observer(t, ctx, "hysteria2")
	wire.mu.Lock()
	wire.cfg.Bindings = []nodeprov.HistoryBinding{
		{Runtime: "xray", InboundTag: "a", LabelKind: "xray_email", Label: "x",
			CredentialRef: uuid(t), Kind: "device", BindingRevision: 1},
		{Runtime: "hysteria2", InboundTag: "b", LabelKind: "hysteria_auth_id", Label: "h",
			CredentialRef: uuid(t), Kind: "device", BindingRevision: 1},
	}
	wire.mu.Unlock()
	m := newManager(t, api, t.TempDir())
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(NewUnixSource("xray", xrayPath)))
	must(t, m.Register(NewUnixSource("hysteria2", hyPath)))
	must(t, m.Poll(ctx))
	stopXray := startSource(t, ctx, m, "xray")
	defer stopXray()
	waitStatus(t, m, func(s Status) bool {
		for _, producer := range s.Producers {
			if producer.ProducerID == "xray" {
				return producer.AppliedRevision == 1
			}
		}
		return false
	})
	if status := m.Status(); status.State == "collecting" || status.AppliedRevision != 0 {
		t.Fatalf("claimed applied before all suppliers: %+v", status)
	}
	// When the second exporter durably crosses the same desired barrier.
	stopHy := startSource(t, ctx, m, "hysteria2")
	defer stopHy()
	waitStatus(t, m, func(s Status) bool { return s.State == "collecting" && s.AppliedRevision == 1 })
	wire.mu.Lock()
	wire.fail = true
	wire.mu.Unlock()
	if err := m.Poll(ctx); err == nil {
		t.Fatal("poll failure was hidden")
	}
	// Then polling failure retains applied state and exposes desired freshness.
	s := m.Status()
	if s.DesiredRevision != 1 || s.AppliedRevision != 1 || s.LastConfigFetchAt == nil ||
		s.ErrorCode != "config_unavailable" || !xray.Hello().Enabled {
		t.Fatalf("poll error changed last policy: %+v", s)
	}
}
