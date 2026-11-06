package history

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
)

func TestUDPDestinationHistoryClearedAtDisable(t *testing.T) {
	// Given an authenticated association created while collection is OFF.
	var err error
	observer, err = ch.NewObserver(ch.Options{ProducerID: "xray", Runtime: "xray", RuntimeBuild: Build})
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := Attach(context.Background())
	ctx = session.ContextWithInbound(ctx, &session.Inbound{Tag: "unit", User: &protocol.MemoryUser{Email: "unit"}})
	Authenticate(ctx, "vless", false, false)
	target := net.UDPDestination(net.LocalHostIP, 53)
	a := newAssociation(ctx, target)
	packet := func(destination net.Destination) {
		t.Helper()
		b := buf.New()
		if _, err := b.Write([]byte("payload")); err != nil {
			t.Fatal(err)
		}
		b.UDP = &destination
		a.observe(buf.MultiBuffer{b}, true)
		b.Release()
	}
	for i := range 100 {
		packet(net.UDPDestination(net.DomainAddress(fmt.Sprintf("off-%d.example", i)), 53))
	}
	if len(a.flows) != 0 {
		t.Fatalf("OFF retained %d history-only destinations", len(a.flows))
	}
	epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	apply := nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: []nodeprov.HistoryBinding{}}
	if _, err := observer.Apply(apply); err != nil {
		t.Fatal(err)
	}
	packet(target)
	oldID := a.flows[target.String()].ID()
	// When disable crosses its barrier, all UDP history metadata is retired.
	apply.ConfigRevision, apply.Enabled = 2, false
	if _, err := observer.Apply(apply); err != nil {
		t.Fatal(err)
	}
	if len(a.flows) != 0 || a.requestCount != 0 || a.domainRequested {
		t.Fatal("disable retained UDP destination metadata")
	}
	// Then re-enable discovers only subsequent traffic with a new partial ID.
	next := nodeprov.HistoryUUID("44444444-4444-4444-8444-444444444444")
	apply.ConfigRevision, apply.Enabled, apply.CollectionEpoch = 3, true, &next
	if _, err := observer.Apply(apply); err != nil {
		t.Fatal(err)
	}
	packet(target)
	if a.flows[target.String()].ID() == oldID {
		t.Fatal("retired destination ID reused")
	}
	starts := 0
	for _, e := range replayEvents(t) {
		if e.Kind == "flow_end" {
			t.Fatal("disable fabricated an association end")
		}
		if e.Kind == "flow_start" {
			starts++
			if !e.Lifecycle.PartialStart || e.Lifecycle.StartedAt != nil || *e.Bytes.Upload != 0 || *e.Bytes.Download != 0 {
				t.Fatal("rediscovered flow lacks partial zero baseline")
			}
		}
	}
	if starts != 2 {
		t.Fatalf("flow starts=%d, expected only ON discoveries", starts)
	}
}
