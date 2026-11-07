package handlers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestConnectionHistoryHTTPExporterOverflowOFF(t *testing.T) {
	// Given real authenticated HTTP routes/PostgreSQL and the production 8 MiB ring.
	f := historyFixture(t)
	request := overflowHTTPRequest(t, f)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	f.enable()
	now := f.now
	o, err := connectionhistory.NewObserver(connectionhistory.Options{
		ProducerID: "xray", Runtime: "xray", RuntimeBuild: "overflow-off-http",
		Capabilities: []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"},
		Clock:        func() time.Time { return now },
	})
	check(err)
	apply := func() nodeprov.HistoryApplied {
		t.Helper()
		command := nodeprov.HistoryApply{Op: "apply", ConfigRevision: f.config.Revision,
			CollectionEpoch: f.config.CollectionEpoch, Enabled: f.config.Enabled, Bindings: f.config.Bindings}
		t.Logf("EXPORTER APPLY input=%+v", command)
		applied, err := o.Apply(command)
		check(err)
		t.Logf("EXPORTER APPLIED %+v", applied)
		return applied
	}
	resume := func(after nodeprov.HistoryU64) [][]byte {
		t.Helper()
		lines, err := o.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.BootID(), AfterSequence: after})
		check(err)
		return lines
	}
	upload := func(lines [][]byte, want int) []byte {
		t.Helper()
		// Preserve each emitted JSON object byte-for-byte; only add batch framing.
		raw := make([][]byte, 0, len(lines))
		for _, line := range lines {
			t.Logf("EXPORTER FRAME %s", bytes.TrimSpace(line))
			raw = append(raw, bytes.TrimSuffix(line, []byte{'\n'}))
		}
		events := append(append([]byte{'['}, bytes.Join(raw, []byte{','})...), ']')
		canonical, err := nodeprov.CanonicalHistoryJSON(events)
		check(err)
		sum := sha256.Sum256(canonical)
		digest := hex.EncodeToString(sum[:])
		body := []byte(fmt.Sprintf(`{"schema_version":1,"batch_id":%q,"payload_sha256":%q,"events":%s}`, historyUUID(t), digest, events))
		return request("POST", "/api/v1/nodes/"+f.node+"/connection-history", body, want)
	}
	on := apply()
	enableSize := len(resume(0)[0])
	tunnel, err := o.NewTunnel(connectionhistory.TunnelInfo{
		Protocol: "vless", InboundTag: f.binding.InboundTag, LabelKind: f.binding.LabelKind, Label: f.binding.Label,
	})
	check(err)
	flow, err := o.NewFlow(connectionhistory.FlowInfo{Tunnel: tunnel, Destination: nodeprov.HistoryDestination{
		RequestedHost: historyPtr("overflow.example.test"), RequestedPort: historyPtr(443), Transport: "tcp",
	}})
	check(err)
	check(flow.Observe(19, 7))
	now = now.Add(connectionhistory.UpdateInterval)
	check(o.Snapshot())
	upload(resume(0), 200)
	prefix := o.Hello().LastSequence
	check(o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.BootID(), ThroughSequence: prefix}))
	check(flow.Close(connectionhistory.End{Reason: "eof", Semantics: "local_owner_closed"}))
	check(tunnel.Close(connectionhistory.End{Reason: "transport_close", Semantics: "local_owner_closed"}))
	if !errors.Is(flow.Observe(1, 1), connectionhistory.ErrClosed) ||
		!errors.Is(tunnel.Close(connectionhistory.End{Reason: "eof", Semantics: "local_owner_closed"}), connectionhistory.ErrClosed) {
		t.Fatal("fixture owners are not retired")
	}
	queued, priorOverflows := 0, 0
	cursor := prefix
	for range 20000 {
		for _, line := range resume(cursor) {
			event, err := nodeprov.DecodeHistoryEvent(line)
			check(err)
			if event.Gap != nil && event.Gap.Reason == "export_overflow" {
				queued = 0
				priorOverflows++
			}
			queued += len(line)
			cursor = event.Sequence
		}
		// A disabled boundary is at least as large as the initial enabled one.
		if connectionhistory.ReplayCapacity-queued < enableSize {
			break
		}
		check(o.ReportGap(nodeprov.HistoryGap{ProducerID: "xray", ObserverBootID: o.BootID(), Reason: "capture_drop"}))
	}
	if queued > connectionhistory.ReplayCapacity || connectionhistory.ReplayCapacity-queued >= enableSize {
		t.Fatalf("failed to reach boundary exhaustion: queued=%d", queued)
	}
	before := o.Hello().LastSequence
	t.Logf("BOUNDARY_PRODUCTION capacity=%d queued=%d remaining=%d enabled_frame=%d prior_overflows=%d before=%s live_owners=0",
		connectionhistory.ReplayCapacity, queued, connectionhistory.ReplayCapacity-queued, enableSize, priorOverflows, before)

	// When the actual disabling boundary exhausts replay with no live owners.
	command, err := json.Marshal(services.ConnectionHistoryBulkCommand{
		Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: f.config.Revision}}, Enabled: false,
	})
	check(err)
	request("POST", "/api/v1/admin/nodes/connection-logging", command, 200)
	check(json.Unmarshal(request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, 200), &f.config))
	off := apply()
	lines := resume(prefix)
	// RED must fail at strict production ingestion, before structural assertions.
	ackRaw := upload(lines, 200)

	// Then unmodified emitted loss and stop drain; only the stop uses OFF revision.
	var ack struct {
		Status string `json:"status"`
	}
	check(json.Unmarshal(ackRaw, &ack))
	if ack.Status != "committed" || len(lines) != 2 {
		t.Fatalf("drain ack=%s frames=%d", ackRaw, len(lines))
	}
	gap, err := nodeprov.DecodeHistoryEvent(lines[0])
	check(err)
	stop, err := nodeprov.DecodeHistoryEvent(lines[1])
	check(err)
	if gap.Gap == nil || gap.Gap.Reason != "export_overflow" || gap.ConfigRevision != on.ConfigRevision ||
		*gap.Gap.FirstSequence != prefix+1 || *gap.Gap.LastSequence != gap.Sequence-1 ||
		*gap.Gap.LastSequence < before || stop.Boundary == nil || stop.Boundary.Action != "disabled" ||
		stop.Boundary.PriorRevision != on.ConfigRevision || stop.ConfigRevision != off.ConfigRevision ||
		stop.Sequence != gap.Sequence+1 || stop.Sequence != off.BarrierSequence || off.Enabled {
		t.Fatalf("invalid loss/stop gap=%+v stop=%+v applied=%+v", gap, stop, off)
	}
	check(o.Ack(nodeprov.HistoryAck{Op: "ack", ObserverBootID: o.BootID(), ThroughSequence: stop.Sequence}))
	check(o.Snapshot())
	check(o.ReportGap(nodeprov.HistoryGap{ProducerID: "xray", ObserverBootID: o.BootID(), Reason: "capture_drop"}))
	if len(resume(stop.Sequence)) != 0 || o.Hello().Enabled || o.Hello().FirstAvailableSequence != stop.Sequence+1 {
		t.Fatal("ACK did not empty replay or OFF emitted new detail")
	}
	if again := apply(); again.BarrierSequence != stop.Sequence {
		t.Fatal("idempotent OFF changed the barrier")
	}
	// Negative controls are intentionally forged copies, never the positive stream.
	for _, kind := range []string{"gap", "detail"} {
		bad := gap
		if kind == "detail" {
			bad = f.event(uint64(stop.Sequence+1), "flow_update", 99, 1, 1)
		}
		bad.Sequence, bad.EventID, bad.ConfigRevision = stop.Sequence+1, string(bad.ObserverBootID)+":"+(stop.Sequence+1).String(), off.ConfigRevision
		body, err := json.Marshal(f.batch(bad))
		check(err)
		if raw := request("POST", "/api/v1/nodes/"+f.node+"/connection-history", body, 409); !bytes.Contains(raw, []byte("revision_disabled")) {
			t.Fatalf("negative %s control=%s", kind, raw)
		}
	}
	for _, resource := range []string{"flows", "sessions"} {
		var page services.ConnectionHistoryPage
		check(json.Unmarshal(request("GET", "/api/v1/admin/connection-history/"+resource+"?status=collection_stopped", nil, 200), &page))
		if len(page.Items) != 1 {
			t.Fatalf("%s stopped rows=%+v", resource, page)
		}
		row := page.Items[0]
		if row.Event.Lifecycle.EndedAt != nil || row.Event.Bytes.Final || row.Event.Quality.Completeness != "unknown" || len(row.GapCoverage) != 1 {
			t.Fatalf("%s fabricated terminal or concealed loss: %+v", resource, row)
		}
	}
	check(f.svc.RecordStatus(t.Context(), f.node, services.ConnectionHistoryStatus{
		DesiredRevision: off.ConfigRevision, AppliedRevision: off.ConfigRevision, CollectionEpoch: off.CollectionEpoch,
		State: "off", AppliedAt: historyPtr(nodeprov.HistoryTimestamp(now)), Producers: []services.ConnectionHistoryProducerStatus{{
			ProducerID: "xray", ObserverBootID: historyPtr(o.BootID()), RuntimeBuild: "overflow-off-http",
			Capabilities: o.Hello().Capabilities, AppliedRevision: off.ConfigRevision, LastDurableSequence: stop.Sequence,
		}},
	}))
	control := request("GET", "/api/v1/admin/nodes/"+f.node+"/connection-logging", nil, 200)
	for _, want := range []string{`"state":"off"`, `"enabled":false`, `"applied_enabled":false`, `"queue_bytes":"0"`} {
		if !bytes.Contains(control, []byte(want)) {
			t.Fatalf("OFF control missing %s: %s", want, control)
		}
	}
	t.Logf("OVERFLOW_OFF_HTTP_PASS gap=%s stop=%s durable_ack=%s queue=0 negative_controls=409 stopped_flows=1 stopped_sessions=1", gap.Sequence, stop.Sequence, stop.Sequence)
}

func overflowHTTPRequest(t *testing.T, f *historyHTTPFixture) func(string, string, []byte, int) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready, stopped := make(chan struct{}), make(chan error, 1)
	f.app.Hooks().OnListen(func(fiber.ListenData) error { close(ready); return nil })
	go func() { stopped <- f.app.Listener(listener) }()
	t.Cleanup(func() {
		if err := f.app.ShutdownWithTimeout(5 * time.Second); err != nil {
			t.Error(err)
		}
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error(ctx.Err())
		}
		t.Logf("CLEANUP HTTP listener=%s closed", listener.Addr())
	})
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	client := &http.Client{Timeout: 15 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	return func(method, path string, body []byte, want int) []byte {
		t.Helper()
		t.Logf("TCP HTTP REQUEST %s %s body=%s", method, path, body)
		req, err := http.NewRequestWithContext(ctx, method, "http://"+listener.Addr().String()+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-Key", "history-key-0")
		if strings.HasPrefix(path, "/api/v1/admin") {
			req.Header.Set("Authorization", "Bearer "+f.token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("TCP HTTP RESPONSE status=%d body=%s DB=%s", resp.StatusCode, data, f.dbState())
		if resp.StatusCode != want {
			t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, want, data)
		}
		return data
	}
}
