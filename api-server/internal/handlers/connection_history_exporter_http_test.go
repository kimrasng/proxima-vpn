package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestConnectionHistoryHTTPExporterDisableAndReenable(t *testing.T) {
	for _, order := range []string{"forward", "reverse"} {
		t.Run(order, func(t *testing.T) {
			// Given: real server-issued policy/bindings and a shared exporter,
			// with synchronous callbacks and an explicitly advanced sample clock.
			f := historyFixture(t)
			f.enable()
			now := f.now
			o, err := connectionhistory.NewObserver(connectionhistory.Options{
				ProducerID: "xray", Runtime: "xray", RuntimeBuild: "shared-observer-http-regression",
				Capabilities: []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"},
				Clock:        func() time.Time { return now },
			})
			check := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			check(err)
			apply := func() nodeprov.HistoryApplied {
				t.Helper()
				command := nodeprov.HistoryApply{
					Op: "apply", ConfigRevision: f.config.Revision, Enabled: f.config.Enabled,
					CollectionEpoch: f.config.CollectionEpoch, Bindings: f.config.Bindings,
				}
				raw, err := json.Marshal(command)
				check(err)
				wire, err := nodeprov.DecodeHistoryControl(append(raw, '\n'))
				check(err)
				decoded, ok := wire.(nodeprov.HistoryApply)
				if !ok {
					t.Fatalf("apply wire=%T", wire)
				}
				applied, err := o.Apply(decoded)
				check(err)
				t.Logf("EXPORTER APPLY command=%s applied=%+v", raw, applied)
				if applied.ConfigRevision != f.config.Revision || applied.Enabled != f.config.Enabled ||
					applied.CollectionEpoch == nil || *applied.CollectionEpoch != *f.config.CollectionEpoch {
					t.Fatalf("applied=%+v config=%+v", applied, f.config)
				}
				return applied
			}
			toggle := func(enabled bool) nodeprov.HistoryApplied {
				t.Helper()
				f.request("POST", "/api/v1/admin/nodes/connection-logging", services.ConnectionHistoryBulkCommand{
					Nodes:   []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: f.config.Revision}},
					Enabled: enabled,
				}, "", 200)
				raw := f.request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, "history-key-0", 200)
				check(json.Unmarshal(raw, &f.config))
				return apply()
			}
			events := func(after nodeprov.HistoryU64) []nodeprov.HistoryEvent {
				t.Helper()
				frames, err := o.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.BootID(), AfterSequence: after})
				check(err)
				out := make([]nodeprov.HistoryEvent, 0, len(frames))
				for _, line := range frames {
					event, err := nodeprov.DecodeHistoryEvent(line)
					check(err)
					t.Logf("EXPORTER FRAME %s", bytes.TrimSpace(line))
					out = append(out, event)
				}
				return out
			}
			ingest := func(exported []nodeprov.HistoryEvent) {
				t.Helper()
				if order == "reverse" {
					slices.Reverse(exported)
				}
				// Only delivery order changes. Never rewrite exporter revisions,
				// epochs, identities, counters or boundary payloads.
				body := f.ingest(f.batch(exported...), 200)
				if !bytes.Contains(body, []byte(`"status":"committed"`)) {
					t.Fatalf("exporter ACK=%s", body)
				}
			}
			first := apply()
			tunnel, err := o.NewTunnel(connectionhistory.TunnelInfo{
				Protocol: "vless", InboundTag: f.binding.InboundTag,
				LabelKind: f.binding.LabelKind, Label: f.binding.Label,
				Peer: &nodeprov.HistoryPeer{IP: "198.51.100.3", Port: 53127},
			})
			check(err)
			flow, err := o.NewFlow(connectionhistory.FlowInfo{
				Tunnel: tunnel,
				Destination: nodeprov.HistoryDestination{
					RequestedHost: historyPtr("exporter.example.test"), RequestedPort: historyPtr(443), Transport: "tcp",
				},
			})
			check(err)
			check(flow.Observe(17, 23))
			now = now.Add(connectionhistory.UpdateInterval)
			check(o.Snapshot())
			ingest(events(0))
			beforeDisable := o.Hello().LastSequence
			check(flow.Observe(12, 8))

			// When: disabling a live flow emits its final nonterminal samples,
			// then the new-revision boundary. Upload while desired OFF is valid.
			off := toggle(false)
			disabled := events(beforeDisable)
			if len(disabled) != 3 {
				t.Fatalf("disable frames=%d want flow/tunnel updates and boundary", len(disabled))
			}
			// This HTTP call must reject the pre-fix Observer's authentic batch
			// with revision_disabled, rather than passing on handcrafted events.
			ingest(slices.Clone(disabled))

			// Then: updates retain the active revision; only the boundary and
			// applied response use the disabling revision. No end is invented.
			for _, event := range disabled[:2] {
				if event.ConfigRevision != first.ConfigRevision || event.CollectionEpoch != *first.CollectionEpoch ||
					event.Lifecycle.EndedAt != nil || event.Bytes.Final {
					t.Fatalf("disable snapshot=%+v", event)
				}
			}
			boundary := disabled[2]
			if disabled[0].Kind != "flow_update" || disabled[1].Kind != "tunnel_update" ||
				boundary.Boundary == nil || boundary.Boundary.Action != "disabled" ||
				boundary.ConfigRevision != off.ConfigRevision || boundary.Boundary.PriorRevision != first.ConfigRevision ||
				boundary.Sequence != off.BarrierSequence {
				t.Fatalf("disable ordering=%+v applied=%+v", disabled, off)
			}
			oldPage := f.page("?collection_epoch=" + string(*first.CollectionEpoch))
			if len(oldPage.Items) != 1 {
				t.Fatalf("old epoch rows=%+v", oldPage)
			}
			old := oldPage.Items[0]
			if old.ObservationStatus != "collection_stopped" || old.Event.Lifecycle.EndedAt != nil || old.Event.Bytes.Final ||
				*old.Event.Bytes.Upload != 29 || *old.Event.Bytes.Download != 31 ||
				old.Event.Auth.CredentialRef == nil || *old.Event.Auth.CredentialRef != f.binding.CredentialRef {
				t.Fatalf("old nonterminal projection=%+v", old)
			}
			check(flow.Observe(900, 800))
			check(o.Snapshot())
			if got := events(off.BarrierSequence); len(got) != 0 {
				t.Fatalf("OFF emitted frames=%+v", got)
			}

			// Re-enable the same live owners under the actual new server epoch.
			on := toggle(true)
			if *on.CollectionEpoch == *first.CollectionEpoch || o.BootID() != boundary.ObserverBootID {
				t.Fatalf("epoch/boot not preserved across toggle: first=%+v on=%+v", first, on)
			}
			baseline := events(off.BarrierSequence)
			if len(baseline) != 3 || baseline[0].Boundary == nil || baseline[0].Boundary.Action != "enabled" ||
				baseline[1].Kind != "tunnel_start" || baseline[2].Kind != "flow_start" ||
				!baseline[2].Lifecycle.PartialStart || baseline[2].Lifecycle.StartedAt != nil ||
				*baseline[2].Bytes.Upload != 0 || *baseline[2].Bytes.Download != 0 {
				t.Fatalf("reenable baseline=%+v", baseline)
			}
			check(flow.Observe(7, 5))
			end := connectionhistory.End{Reason: "eof", Semantics: "local_owner_closed"}
			check(flow.Close(end))
			check(tunnel.Close(end))
			// Reverse delivery includes terminal-before-start and boundary-last.
			ingest(events(off.BarrierSequence))
			newPage := f.page("?collection_epoch=" + string(*on.CollectionEpoch))
			if len(newPage.Items) != 1 {
				t.Fatalf("new epoch rows=%+v", newPage)
			}
			current := newPage.Items[0]
			if current.ObservationStatus != "ended" || current.Event.Lifecycle.EndedAt == nil || !current.Event.Bytes.Final ||
				*current.Event.Bytes.Upload != 7 || *current.Event.Bytes.Download != 5 ||
				*current.Event.FlowID != flow.ID() || *current.Event.TunnelID != tunnel.ID() ||
				current.Event.Auth.CredentialRef == nil || *current.Event.Auth.CredentialRef != f.binding.CredentialRef {
				t.Fatalf("new epoch projection=%+v", current)
			}
			var quota, traffic int64
			check(f.db.QueryRow(context.Background(), `SELECT traffic_used,(SELECT sum(up_bytes+dn_bytes) FROM traffic_logs) FROM users WHERE id=$1`, f.user).Scan(&quota, &traffic))
			if quota != 713 || traffic != 228 {
				t.Fatalf("history changed billing: quota=%d traffic=%d", quota, traffic)
			}
		})
	}
}
