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

func TestAmbiguousFullConeReplyDoesNotGuessRequestedDomain(t *testing.T) {
	// Given multiple requested names and an IP sharing an association/port.
	var err error
	observer, err = ch.NewObserver(ch.Options{ProducerID: "xray", Runtime: "xray", RuntimeBuild: Build})
	if err != nil {
		t.Fatal(err)
	}
	epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	apply := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: []nodeprov.HistoryBinding{}}
	if _, err := observer.Apply(apply); err != nil {
		t.Fatal(err)
	}
	ctx, _ := Attach(context.Background())
	ctx = session.ContextWithInbound(ctx, &session.Inbound{Tag: "unit", User: &protocol.MemoryUser{Email: "unit"}})
	Authenticate(ctx, "vless", false, false)
	upReader, upWriter := pipe.New()
	downReader, downWriter := pipe.New()
	link := &transport.Link{Reader: downReader, Writer: upWriter}
	target := net.UDPDestination(net.DomainAddress("First.Example"), 53)
	WrapDispatch(ctx, target, link)
	remote := net.UDPDestination(net.LocalHostIP, 53)
	for _, d := range []net.Destination{target, net.UDPDestination(net.DomainAddress("Second.Example"), 53), remote} {
		b := buf.New()
		b.Write([]byte("request"))
		b.UDP = &d
		if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
			t.Fatal(err)
		}
		sent, err := upReader.ReadMultiBuffer()
		if err != nil {
			t.Fatal(err)
		}
		buf.ReleaseMulti(sent)
	}
	// When an IP response cannot prove which original request it answers.
	b := buf.New()
	b.Write([]byte("response"))
	b.UDP = &remote
	if err := downWriter.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
		t.Fatal(err)
	}
	reply, err := link.Reader.ReadMultiBuffer()
	if err != nil {
		t.Fatal(err)
	}
	buf.ReleaseMulti(reply)
	apply.ConfigRevision, apply.Enabled = 2, false
	if _, err := observer.Apply(apply); err != nil {
		t.Fatal(err)
	}
	// Then bytes appear once on a response-only flow, never a guessed domain.
	responses := 0
	for _, e := range replayEvents(t) {
		if e.Kind != "flow_update" {
			continue
		}
		if e.Destination.RequestedHost != nil {
			if *e.Bytes.Download != 0 {
				t.Fatal("ambiguous response guessed a request")
			}
			continue
		}
		if e.Destination.ObservedIP == nil || *e.Destination.ObservedIP != "127.0.0.1" || *e.Bytes.Download != 8 {
			t.Fatal("missing response-only IP observation")
		}
		responses++
	}
	if responses != 1 {
		t.Fatalf("response-only flows=%d", responses)
	}
	upReader.Interrupt()
	downReader.Interrupt()
}
