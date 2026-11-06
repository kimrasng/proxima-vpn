package server

import (
	"fmt"
	"sync"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/stretchr/testify/require"
)

func historyUDPFixture(t *testing.T, enabled bool) (*connectionhistory.Observer, *connectionhistory.Tunnel, *udpHistory) {
	t.Helper()
	o, err := connectionhistory.NewObserver(connectionhistory.Options{
		ProducerID: "hysteria2", Runtime: "hysteria2", RuntimeBuild: "test",
	})
	require.NoError(t, err)
	if enabled {
		historyUDPApply(t, o, 1, "11111111-1111-4111-8111-111111111111", true)
	}
	tunnel, err := o.NewTunnel(connectionhistory.TunnelInfo{
		Protocol: "hysteria2", InboundTag: "test", LabelKind: "hysteria_auth_id", Label: "alice",
	})
	require.NoError(t, err)
	u := newUDPHistory(o, tunnel)
	return o, tunnel, u
}

func historyUDPApply(t *testing.T, o *connectionhistory.Observer, revision nodeprov.HistoryU64, epoch nodeprov.HistoryUUID, enabled bool) nodeprov.HistoryApplied {
	t.Helper()
	a, err := o.Apply(nodeprov.HistoryApply{
		Op: "apply", ConfigRevision: revision, CollectionEpoch: &epoch, Enabled: enabled,
		Bindings: []nodeprov.HistoryBinding{{
			Runtime: "hysteria2", InboundTag: "test", LabelKind: "hysteria_auth_id", Label: "alice",
			CredentialRef: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Kind: "device", BindingRevision: 1,
		}},
	})
	require.NoError(t, err)
	return a
}

func historyUDPAssertEmpty(t *testing.T, u *udpHistory) {
	t.Helper()
	// Call only after synchronous operations or after every callback has joined.
	// Production map access remains exclusively inside the gate/reset callbacks.
	require.Zero(t, len(u.requests))
	require.Zero(t, len(u.effectiveTargets))
	require.Zero(t, len(u.responses))
	require.Nil(t, u.requests)
	require.Nil(t, u.effectiveTargets)
	require.Nil(t, u.responses)
}

func TestHistoryUDPInitiallyOffRetainsNoDestinations(t *testing.T) {
	// Given a live association created while collection is initially OFF.
	_, tunnel, u := historyUDPFixture(t, false)
	defer u.close("transport_close")
	// When requests, rewrites and unsolicited response sources keep changing.
	for i := range 4096 {
		request := fmt.Sprintf("off-%d.test:443", i)
		response := fmt.Sprintf("127.0.0.1:%d", i+1024)
		u.upload(request, 19)
		u.effective(request, response)
		u.download(response, 7)
	}
	// Then neither supplier maps nor shared child owners form an OFF journal.
	historyUDPAssertEmpty(t, u)
	require.NoError(t, tunnel.Close(connectionhistory.End{Reason: "transport_close", Semantics: "local_owner_closed"}))
}

func TestHistoryUDPHeldAssociationDiscardsOffDestinations(t *testing.T) {
	// Given one observed association with request and response-only owners.
	o, tunnel, u := historyUDPFixture(t, true)
	u.upload("127.0.0.1:1234", 19)
	u.effective("127.0.0.1:1234", "127.0.0.1:1234")
	u.download("127.0.0.2:1234", 7)
	before := historyEvents(t, o)
	oldIDs := make(map[string]bool)
	for _, e := range before {
		if e.FlowID != nil {
			oldIDs[*e.FlowID] = true
		}
	}
	require.Len(t, oldIDs, 2)
	// When OFF is applied and the same association carries new destinations.
	off := historyUDPApply(t, o, 2, "11111111-1111-4111-8111-111111111111", false)
	historyUDPAssertEmpty(t, u)
	for i := range 4096 {
		request := fmt.Sprintf("off-%d.test:443", i)
		response := fmt.Sprintf("127.0.0.3:%d", i+1024)
		u.upload(request, 23)
		u.effective(request, response)
		u.download(response, 11)
	}
	historyUDPAssertEmpty(t, u)
	epoch := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	on := historyUDPApply(t, o, 3, epoch, true)
	// Then enable has no forgotten destination snapshots to replay.
	for _, e := range historyEvents(t, o) {
		if e.Sequence > off.BarrierSequence && e.Sequence <= on.BarrierSequence {
			require.Nil(t, e.FlowID)
		}
	}
	u.upload("127.0.0.1:1234", 13)
	u.download("127.0.0.1:1234", 17)
	u.close("transport_close")
	var starts, ends int
	for _, e := range historyEvents(t, o) {
		if e.FlowID == nil {
			continue
		}
		if oldIDs[*e.FlowID] {
			require.NotEqual(t, "flow_end", e.Kind)
			continue
		}
		require.Equal(t, epoch, e.CollectionEpoch)
		require.Equal(t, tunnel.ID(), *e.TunnelID)
		require.Equal(t, nodeprov.HistoryUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), *e.Auth.CredentialRef)
		require.True(t, e.Lifecycle.PartialStart)
		require.Nil(t, e.Lifecycle.StartedAt)
		switch e.Kind {
		case "flow_start":
			starts++
			require.Zero(t, *e.Bytes.Upload)
			require.Zero(t, *e.Bytes.Download)
		case "flow_end":
			ends++
			require.True(t, e.Bytes.Final)
			if e.Destination.RequestedHost == nil {
				require.Zero(t, *e.Bytes.Upload)
				require.EqualValues(t, 17, *e.Bytes.Download)
			} else {
				require.EqualValues(t, 13, *e.Bytes.Upload)
				require.Zero(t, *e.Bytes.Download)
			}
		}
	}
	require.Equal(t, 2, starts)
	require.Equal(t, 2, ends)
	require.NoError(t, tunnel.Close(connectionhistory.End{Reason: "transport_close", Semantics: "local_owner_closed"}))
}

func TestHistoryUDPInitiallyOffUsesPartialSegments(t *testing.T) {
	// Given an association and unknown authenticated binding created while OFF.
	o, tunnel, u := historyUDPFixture(t, false)
	u.upload("unobserved.test:443", 99)
	// When the first manifest enables it and a request/reply is observed.
	historyUDPApply(t, o, 1, "11111111-1111-4111-8111-111111111111", true)
	u.upload("127.0.0.1:1234", 19)
	u.download("127.0.0.1:1234", 7)
	u.close("transport_close")
	// Then the absent OFF history cannot establish exact starts or reply joins.
	var starts, ends int
	for _, e := range historyEvents(t, o) {
		if e.FlowID == nil {
			continue
		}
		require.Equal(t, tunnel.ID(), *e.TunnelID)
		require.True(t, e.Lifecycle.PartialStart)
		require.Nil(t, e.Lifecycle.StartedAt)
		require.Equal(t, "device", e.Auth.Kind)
		switch e.Kind {
		case "flow_start":
			starts++
			require.Zero(t, *e.Bytes.Upload)
			require.Zero(t, *e.Bytes.Download)
		case "flow_end":
			ends++
			if e.Destination.RequestedHost == nil {
				require.EqualValues(t, 7, *e.Bytes.Download)
			} else {
				require.Equal(t, "127.0.0.1", *e.Destination.RequestedHost)
				require.EqualValues(t, 19, *e.Bytes.Upload)
				require.Zero(t, *e.Bytes.Download)
			}
		}
	}
	require.Equal(t, 2, starts)
	require.Equal(t, 2, ends)
}

func TestHistoryUDPConcurrentDisableRetainsNothing(t *testing.T) {
	// Given an observed association and concurrent callbacks at the toggle gate.
	o, tunnel, u := historyUDPFixture(t, true)
	defer u.close("transport_close")
	epoch := nodeprov.HistoryUUID("11111111-1111-4111-8111-111111111111")
	for i := range 20 {
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			u.upload("new.test:443", 19)
			u.effective("new.test:443", "127.0.0.1:443")
			u.download("127.0.0.2:443", 7)
		}()
		// When disable races creation/refinement/response callbacks, either order
		// has the same postcondition; no scheduling outcome makes the test pass.
		close(start)
		historyUDPApply(t, o, nodeprov.HistoryU64(2*i+2), epoch, false)
		wg.Wait()
		// Then Apply's completed OFF barrier leaves exactly zero retained keys.
		historyUDPAssertEmpty(t, u)
		if i < 19 {
			epoch = nodeprov.HistoryUUID(fmt.Sprintf("11111111-1111-4111-8111-%012d", i+1))
			historyUDPApply(t, o, nodeprov.HistoryU64(2*i+3), epoch, true)
		}
	}
	require.NoError(t, tunnel.Close(connectionhistory.End{Reason: "transport_close", Semantics: "local_owner_closed"}))
}
