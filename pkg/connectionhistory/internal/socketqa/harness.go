// Package socketqa drives the exported API and actual Unix socket for acceptance.
package socketqa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// Run owns a real listener and three collector sessions. It writes actual wire
// JSONL to output, not reconstructed snapshots, and asserts the byte contract.
func Run(ctx context.Context, dir string, output io.Writer) (err error) {
	o, err := connectionhistory.NewObserver(connectionhistory.Options{
		ProducerID: "xray", Runtime: "xray", RuntimeBuild: "shared-socket-qa",
		Capabilities: []string{"auth_transport", "hot_toggle", "tcp_flow"},
	})
	if err != nil {
		return err
	}
	tunnel, err := o.NewTunnel(connectionhistory.TunnelInfo{Protocol: "vless", InboundTag: "qa", LabelKind: "xray_email", Label: "private-authenticated-label"})
	if err != nil {
		return err
	}
	flow, err := o.NewFlow(connectionhistory.FlowInfo{Tunnel: tunnel, Destination: nodeprov.HistoryDestination{RequestedHost: value("example.test"), RequestedPort: value(443), Transport: "tcp"}})
	if err != nil {
		return err
	}
	if err := flow.Observe(900, 900); err != nil {
		return err
	}
	path := filepath.Join(dir, "private", "history.sock")
	server, err := o.Listen(ctx, path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, server.Close()) }()
	c, hello, err := connect(ctx, path, output)
	if err != nil {
		return err
	}
	defer func() {
		if c != nil {
			err = errors.Join(err, c.conn.Close())
		}
	}()
	if hello.Enabled || hello.LastSequence != 0 {
		return fmt.Errorf("not initially OFF")
	}
	if err := c.send(nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID}); err != nil {
		return err
	}
	epoch1 := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	epoch2 := nodeprov.HistoryUUID("44444444-4444-4444-8444-444444444444")
	apply := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch1, Enabled: true, Bindings: []nodeprov.HistoryBinding{}}
	if err := c.send(apply); err != nil {
		return err
	}
	first, _, err := c.untilApplied()
	if err != nil {
		return err
	}
	if err := zeroBaseline(first, flow.ID()); err != nil {
		return err
	}
	if err := flow.Observe(12, 3); err != nil {
		return err
	}
	apply.ConfigRevision, apply.Enabled = 2, false
	if err := c.send(apply); err != nil {
		return err
	}
	preOff, off, err := c.untilApplied()
	if err != nil {
		return err
	}
	var found bool
	for _, e := range preOff {
		if e.Kind == "flow_update" {
			found = true
			if *e.Bytes.Upload != 12 || *e.Bytes.Download != 3 || e.Bytes.Final || e.Lifecycle.EndedAt != nil {
				return fmt.Errorf("disable terminal/bytes mismatch")
			}
		}
	}
	if !found {
		return fmt.Errorf("missing disable update")
	}
	if err := flow.Observe(500, 500); err != nil {
		return err
	}
	if o.Hello().LastSequence != off.BarrierSequence {
		return fmt.Errorf("post-disable event")
	}
	if err := c.send(nodeprov.HistoryAck{Op: "ack", ObserverBootID: hello.ObserverBootID, ThroughSequence: off.BarrierSequence}); err != nil {
		return err
	}
	// An idempotent apply is an ordered command fence proving ACK processing,
	// without relying on a delay or a nonexistent ACK response frame.
	if err := c.send(apply); err != nil {
		return err
	}
	if _, _, err := c.untilApplied(); err != nil {
		return err
	}
	if err := c.conn.Close(); err != nil {
		return err
	}
	c = nil
	c, again, err := connect(ctx, path, output)
	if err != nil {
		return err
	}
	if again.ObserverBootID != hello.ObserverBootID || again.FirstAvailableSequence != off.BarrierSequence+1 {
		return fmt.Errorf("reconnect changed boot or lost ACK")
	}
	if err := c.send(nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID, AfterSequence: off.BarrierSequence}); err != nil {
		return err
	}
	apply.ConfigRevision, apply.Enabled, apply.CollectionEpoch = 3, true, &epoch2
	if err := c.send(apply); err != nil {
		return err
	}
	second, _, err := c.untilApplied()
	if err != nil {
		return err
	}
	if err := zeroBaseline(second, flow.ID()); err != nil {
		return err
	}
	// These are real shared callbacks, not a fabricated transcript.
	if err := flow.Observe(12, 3); err != nil {
		return err
	}
	if err := flow.Observe(7, 4); err != nil {
		return err
	}
	if err := flow.Close(connectionhistory.End{Reason: "eof", Semantics: "local_owner_closed"}); err != nil {
		return err
	}
	if err := tunnel.Close(connectionhistory.End{Reason: "transport_close", Semantics: "local_owner_closed"}); err != nil {
		return err
	}
	if err := c.send(apply); err != nil {
		return err
	}
	terminal, _, err := c.untilApplied()
	if err != nil {
		return err
	}
	if err := finalCounters(terminal, flow.ID(), epoch2); err != nil {
		return err
	}
	last := o.Hello().LastSequence
	if err := c.conn.Close(); err != nil {
		return err
	}
	c = nil
	// Simulate lost ACK: reconnect and replay the same epoch from its old
	// durable checkpoint. Every unacknowledged event must be identical.
	c, _, err = connect(ctx, path, output)
	if err != nil {
		return err
	}
	if err := c.send(nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID, AfterSequence: off.BarrierSequence}); err != nil {
		return err
	}
	if err := c.send(apply); err != nil {
		return err
	}
	replayed, _, err := c.untilApplied()
	if err != nil {
		return err
	}
	expected := append(second, terminal...)
	a, err := nodeprov.HistoryEventsDigest(expected)
	if err != nil {
		return err
	}
	b, err := nodeprov.HistoryEventsDigest(replayed)
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("ACK-loss replay changed content")
	}
	if err := c.send(nodeprov.HistoryAck{Op: "ack", ObserverBootID: hello.ObserverBootID, ThroughSequence: last}); err != nil {
		return err
	}
	if err := c.send(apply); err != nil {
		return err
	}
	if _, _, err := c.untilApplied(); err != nil {
		return err
	}
	if o.Hello().FirstAvailableSequence != last+1 {
		return fmt.Errorf("final ACK did not reclaim records")
	}
	return nil
}

func zeroBaseline(events []nodeprov.HistoryEvent, id string) error {
	for _, e := range events {
		if e.Kind == "flow_start" && e.FlowID != nil && *e.FlowID == id {
			if *e.Bytes.Upload != 0 || *e.Bytes.Download != 0 || !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil {
				return fmt.Errorf("fresh epoch baseline mismatch")
			}
			return nil
		}
	}
	return fmt.Errorf("held flow missing on enable")
}
func finalCounters(events []nodeprov.HistoryEvent, id string, epoch nodeprov.HistoryUUID) error {
	var count int
	for _, e := range events {
		if e.Kind == "flow_end" && *e.FlowID == id {
			count++
			if *e.Bytes.Upload != 19 || *e.Bytes.Download != 7 || !e.Bytes.Final || e.CollectionEpoch != epoch {
				return fmt.Errorf("expected final 19 upload / 7 download")
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("terminal count %d, want 1", count)
	}
	return nil
}
