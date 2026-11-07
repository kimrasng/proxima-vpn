package history

import (
	"context"
	"testing"
	"time"

	ch "github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

func TestActivityOwnerRetiresAfterChildren(t *testing.T) {
	for _, name := range []string{"shadowsocks", "shadowsocks2022"} {
		for _, collection := range []string{"on", "off"} {
			for _, identity := range []string{"shared", "unknown"} {
				t.Run(name+"/"+collection+"/"+identity, func(t *testing.T) {
					// Given a real observer and UDP association with a held callback.
					now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
					var err error
					observer, err = ch.NewObserver(ch.Options{
						ProducerID: "xray", Runtime: "xray", RuntimeBuild: Build,
						Clock: func() time.Time { return now },
					})
					if err != nil {
						t.Fatal(err)
					}
					epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
					bindings := []nodeprov.HistoryBinding{}
					if identity == "shared" {
						bindings = append(bindings, nodeprov.HistoryBinding{
							Runtime: "xray", InboundTag: "unit", LabelKind: "shared_inbound",
							Label: "unit", Kind: "shared", BindingRevision: 1,
							CredentialRef: "33333333-3333-4333-8333-333333333333",
						})
					}
					apply := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: bindings}
					if _, err := observer.Apply(apply); err != nil {
						t.Fatal(err)
					}
					ctx, owner := Attach(context.Background())
					ctx = session.ContextWithInbound(ctx, &session.Inbound{Tag: "unit", User: &protocol.MemoryUser{}})
					Authenticate(ctx, name, true, true)
					if owner.tunnel == nil {
						t.Fatal("authenticated activity owner missing")
					}
					id := owner.tunnel.ID()
					upReader, upWriter := pipe.New()
					downReader, downWriter := pipe.New()
					t.Cleanup(upReader.Interrupt)
					t.Cleanup(downReader.Interrupt)
					link := &transport.Link{Reader: downReader, Writer: upWriter}
					_, association := WrapDispatch(ctx, net.UDPDestination(net.LocalHostIP, 53), link)
					done := TrackLink(link)
					if err := link.Writer.WriteMultiBuffer(buf.MergeBytes(nil, []byte("payload"))); err != nil {
						t.Fatal(err)
					}
					packet, err := upReader.ReadMultiBuffer()
					if err != nil {
						t.Fatal(err)
					}
					if err := downWriter.WriteMultiBuffer(packet); err != nil {
						t.Fatal(err)
					}
					reply, err := link.Reader.ReadMultiBuffer()
					if err != nil {
						t.Fatal(err)
					}
					buf.ReleaseMulti(reply)
					association.Routed()
					if collection == "off" {
						apply.ConfigRevision, apply.Enabled = 2, false
						if _, err := observer.Apply(apply); err != nil {
							t.Fatal(err)
						}
					}
					// When the local owner closes before its accepted callback returns.
					owner.Close()
					if err := link.Writer.(*Writer).Close(); err != nil {
						t.Fatal(err)
					}
					link.Reader.(*Reader).Interrupt()
					for _, event := range replayEvents(t) {
						if event.Kind == "flow_end" || event.Kind == "tunnel_end" {
							t.Fatal("terminal event preceded callback quiescence")
						}
					}
					done()
					owner.Close()
					// Then children end first, and the retired owner never reappears.
					flowEnds, tunnelEnds := 0, 0
					var last nodeprov.HistoryU64
					for _, event := range replayEvents(t) {
						last = event.Sequence
						switch event.Kind {
						case "flow_end":
							flowEnds++
							if tunnelEnds != 0 || *event.Bytes.Upload != 7 || *event.Bytes.Download != 7 || !event.Bytes.Final {
								t.Fatal("child finality or ordering violated")
							}
						case "tunnel_end":
							tunnelEnds++
							if flowEnds != 1 || event.Lifecycle.Scope != "activity_window" ||
								*event.Lifecycle.EndSemantics != "observation_segment_closed" ||
								*event.Lifecycle.EndReason != "transport_close" || event.Quality.End != "observed" {
								t.Fatal("activity closure is not an observed local segment")
							}
							if event.Auth.Kind != identity || event.Quality.Identity != "unknown" {
								t.Fatal("shared or unknown account attribution changed")
							}
						}
					}
					if collection == "on" && (flowEnds != 1 || tunnelEnds != 1) {
						t.Fatalf("flow ends=%d, activity ends=%d", flowEnds, tunnelEnds)
					}
					if collection == "off" && (flowEnds != 0 || tunnelEnds != 0) {
						t.Fatal("OFF retirement emitted detail")
					}
					now = now.Add(20 * time.Second)
					if err := observer.Snapshot(); err != nil {
						t.Fatal(err)
					}
					apply.ConfigRevision, apply.Enabled = 3, false
					if _, err := observer.Apply(apply); err != nil {
						t.Fatal(err)
					}
					next := nodeprov.HistoryUUID("44444444-4444-4444-8444-444444444444")
					apply.ConfigRevision, apply.Enabled, apply.CollectionEpoch = 4, true, &next
					if _, err := observer.Apply(apply); err != nil {
						t.Fatal(err)
					}
					for _, event := range replayEvents(t) {
						if event.Sequence > last && event.TunnelID != nil && *event.TunnelID == id {
							t.Fatalf("retired owner reappeared as %s", event.Kind)
						}
					}
				})
			}
		}
	}
}
