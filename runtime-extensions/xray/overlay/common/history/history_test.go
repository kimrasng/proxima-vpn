package history

import (
	"context"
	"testing"

	ch "github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

func TestRetainedDetachWaitsForMeasuredCallback(t *testing.T) {
	// Given a real pipe association with an admitted response callback.
	var err error
	observer, err = ch.NewObserver(ch.Options{ProducerID: "xray", Runtime: "xray", RuntimeBuild: Build})
	if err != nil {
		t.Fatal(err)
	}
	epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	if _, err := observer.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: []nodeprov.HistoryBinding{}}); err != nil {
		t.Fatal(err)
	}
	ctx, owner := Attach(context.Background())
	ctx = session.ContextWithInbound(ctx, &session.Inbound{Tag: "unit", User: &protocol.MemoryUser{Email: "unit"}})
	Authenticate(ctx, "vless", false, false)
	upReader, upWriter := pipe.New()
	downReader, downWriter := pipe.New()
	link := &transport.Link{Reader: downReader, Writer: upWriter}
	_, a := WrapDispatch(ctx, net.UDPDestination(net.LocalHostIP, 9), link)
	Retain(link)
	finishCallback := TrackLink(link)
	if err := link.Writer.WriteMultiBuffer(buf.MergeBytes(nil, []byte("elevenbytes"))); err != nil {
		t.Fatal(err)
	}
	payload, err := upReader.ReadMultiBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if err := downWriter.WriteMultiBuffer(payload); err != nil {
		t.Fatal(err)
	}
	reply, err := link.Reader.ReadMultiBuffer()
	if err != nil {
		t.Fatal(err)
	}
	buf.ReleaseMulti(reply)
	// When the actual carrier closes before its callback has returned.
	owner.Close()
	before := replayEvents(t)
	for _, e := range before {
		if e.Kind == "tunnel_end" || e.Kind == "flow_end" {
			t.Fatal("terminal snapshot preceded callback quiescence")
		}
	}
	finishCallback()
	// Then the old segment and parent close, while the retained pipe stays usable.
	ends := 0
	for _, e := range replayEvents(t) {
		if e.Kind == "flow_end" && e.TunnelID != nil {
			if *e.Bytes.Upload != 11 || *e.Bytes.Download != 11 || *e.Lifecycle.EndSemantics != "observation_segment_closed" {
				t.Fatal("wrong detached segment")
			}
			ends++
		}
		if e.Kind == "tunnel_end" {
			ends++
		}
	}
	if ends != 2 || a.owner != nil {
		t.Fatal("retained association still pins authenticated carrier")
	}
	// The history detach must not close the data-plane pipes.
	if err := upWriter.WriteMultiBuffer(buf.MergeBytes(nil, []byte("still-live"))); err != nil {
		t.Fatal(err)
	}
	b, err := upReader.ReadMultiBuffer()
	if err != nil {
		t.Fatal(err)
	}
	buf.ReleaseMulti(b)
	upReader.Interrupt()
	downReader.Interrupt()
}

func replayEvents(t *testing.T) []nodeprov.HistoryEvent {
	t.Helper()
	lines, err := observer.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: observer.BootID()})
	if err != nil {
		t.Fatal(err)
	}
	var events []nodeprov.HistoryEvent
	for _, line := range lines {
		e, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	return events
}
