package server

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/stretchr/testify/require"
)

func TestHistoryBinary(t *testing.T) {
	// Given the actual patched application and an event reader subscribed before
	// authentication, policy apply, payload, closure, or process termination.
	evidence := os.Getenv("HYSTERIA_EVIDENCE")
	require.NotEmpty(t, evidence)
	target := historyTCPEcho(t)
	udpA, udpB := historyUDPEcho(t), historyUDPEcho(t)
	p := historyStartBinary(t, evidence, "2s")
	c := collectHistory(t, p.socket, filepath.Join(evidence, "events.jsonl"))
	hello := c.hello(t)
	require.False(t, hello.Enabled)
	require.Equal(t, "hysteria2-ca8fbd874fd6-history-v1", hello.RuntimeBuild)
	c.send(t, nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID})
	epoch := nodeprov.HistoryUUID("11111111-1111-4111-8111-111111111111")
	c.apply(t, 1, epoch, true)
	alice, alice2, bob := p.client(t, "alice"), p.client(t, "alice"), p.client(t, "bob")
	tunnels := c.matching(t, "tunnel_start", 3, nil)
	tunnelIDs := make(map[string]bool)
	identities := make(map[nodeprov.HistoryUUID]int)
	for _, e := range tunnels {
		require.Equal(t, "127.0.0.1", e.Peer.IP)
		require.Equal(t, "exact", e.Quality.Identity)
		require.False(t, tunnelIDs[*e.TunnelID])
		tunnelIDs[*e.TunnelID] = true
		identities[*e.Auth.CredentialRef]++
	}
	require.Equal(t, 2, identities["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"])
	require.Equal(t, 1, identities["bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"])

	// When identical destinations, short flows, fragmented multi-destination UDP,
	// live toggles and a final crash traverse the real QUIC process.
	var streams []net.Conn
	for _, cl := range []client.Client{alice, alice, alice2, bob} {
		s, err := cl.TCP(target)
		require.NoError(t, err)
		t.Cleanup(func() { s.Close() })
		historyExchange(t, s)
		streams = append(streams, s)
	}
	starts := c.matching(t, "flow_start", 4, nil)
	require.NotEqual(t, *starts[0].FlowID, *starts[1].FlowID)
	require.Equal(t, *starts[0].TunnelID, *starts[1].TunnelID)
	require.NotEqual(t, *starts[0].TunnelID, *starts[2].TunnelID)
	heldID, heldParent := *starts[0].FlowID, *starts[0].TunnelID
	for _, s := range streams[1:] {
		require.NoError(t, s.Close())
	}
	isTCP := func(e nodeprov.HistoryEvent) bool { return e.Destination.Transport == "tcp" }
	c.matching(t, "flow_end", 3, isTCP)
	for i := range 8 {
		begin := time.Now()
		s, err := alice.TCP(target)
		require.NoError(t, err)
		historyExchange(t, s)
		require.NoError(t, s.Close())
		c.matching(t, "flow_end", 4+i, isTCP)
		require.Less(t, time.Since(begin), time.Second)
	}
	tcpEnds := c.matching(t, "flow_end", 11, isTCP)
	for _, e := range tcpEnds {
		require.True(t, e.Bytes.Final)
		require.Equal(t, nodeprov.HistoryU64(19), *e.Bytes.Upload)
		require.Equal(t, nodeprov.HistoryU64(7), *e.Bytes.Download)
	}
	ua, err := alice.UDP()
	require.NoError(t, err)
	t.Cleanup(func() { ua.Close() })
	ub, err := bob.UDP()
	require.NoError(t, err)
	t.Cleanup(func() { ub.Close() })
	historyUDPExchange(t, ua, udpA, 13)
	historyUDPExchange(t, ua, udpB, 17)
	historyUDPExchange(t, ua, udpA, 3000)
	historyUDPExchange(t, ub, udpA, 23)
	udpEnds := c.matching(t, "flow_end", 3, func(e nodeprov.HistoryEvent) bool { return e.Destination.Transport == "udp" })
	expected := map[uint64]int{3013: 1, 17: 1, 23: 1}
	for _, e := range udpEnds {
		require.True(t, e.Bytes.Final)
		require.Equal(t, "activity_window", e.Lifecycle.Scope)
		require.Equal(t, "activity_expired", *e.Lifecycle.EndSemantics)
		require.Equal(t, "observed", e.Quality.End)
		require.Equal(t, *e.Bytes.Upload, *e.Bytes.Download)
		expected[uint64(*e.Bytes.Upload)]--
	}
	for _, count := range expected {
		require.Zero(t, count)
	}
	historyPutback(t, alice, c)
	pid := p.cmd.Process.Pid
	off := c.apply(t, 2, epoch, false)
	updates := c.matching(t, "flow_update", 1, func(e nodeprov.HistoryEvent) bool { return *e.FlowID == heldID })
	require.False(t, updates[len(updates)-1].Bytes.Final)
	require.Nil(t, updates[len(updates)-1].Lifecycle.EndedAt)
	historyExchange(t, streams[0])
	epoch2 := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	c.apply(t, 3, epoch2, true)
	partial := c.matching(t, "flow_start", 1, func(e nodeprov.HistoryEvent) bool {
		return *e.FlowID == heldID && e.CollectionEpoch == epoch2
	})[0]
	require.True(t, partial.Lifecycle.PartialStart)
	require.Nil(t, partial.Lifecycle.StartedAt)
	require.Zero(t, *partial.Bytes.Upload)
	require.Equal(t, heldParent, *partial.TunnelID)
	require.Equal(t, hello.ObserverBootID, partial.ObserverBootID)
	require.Equal(t, pid, p.cmd.Process.Pid)
	historyExchange(t, streams[0])
	require.NoError(t, streams[0].Close())
	final := c.matching(t, "flow_end", 1, func(e nodeprov.HistoryEvent) bool { return *e.FlowID == heldID })[0]
	require.Equal(t, epoch2, final.CollectionEpoch)
	require.True(t, final.Bytes.Final)
	require.Equal(t, nodeprov.HistoryU64(19), *final.Bytes.Upload)
	require.Equal(t, nodeprov.HistoryU64(7), *final.Bytes.Download)
	require.NoError(t, alice.Close())
	require.NoError(t, alice2.Close())
	require.NoError(t, bob.Close())
	c.matching(t, "tunnel_end", 3, nil)
	crashClient := p.client(t, "alice")
	crashStream, err := crashClient.TCP(target)
	require.NoError(t, err)
	t.Cleanup(func() { crashStream.Close() })
	historyExchange(t, crashStream)
	allStarts := c.matching(t, "flow_start", 1, func(e nodeprov.HistoryEvent) bool {
		return e.CollectionEpoch == epoch2 && !e.Lifecycle.PartialStart
	})
	crashID := *allStarts[len(allStarts)-1].FlowID
	require.NoError(t, p.cmd.Process.Kill())
	select {
	case <-p.done:
		require.Error(t, p.err)
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not exit")
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not observe crash EOF")
	}
	// Then the whole transcript has exactly one end per normally closed flow,
	// no post-OFF details, and no manufactured end for the crashed flow.
	c.mu.Lock()
	events := append([]nodeprov.HistoryEvent(nil), c.events...)
	collectorErr := c.err
	c.mu.Unlock()
	require.ErrorIs(t, collectorErr, io.EOF)
	require.NoError(t, c.file.Sync())
	ended := make(map[string]int)
	for _, e := range events {
		if e.Kind == "flow_end" {
			ended[*e.FlowID]++
		}
		if e.Sequence > off.BarrierSequence && e.CollectionEpoch == epoch {
			t.Fatalf("post-disable detailed event: %s", e.EventID)
		}
	}
	require.Zero(t, ended[crashID])
	require.Len(t, ended, 16)
	for _, count := range ended {
		require.Equal(t, 1, count)
	}
	t.Logf("PRECISE_PASS pid=%d boot=%s tunnels=4 completed_tcp=13 completed_udp=3 crash_flow=%s", pid, hello.ObserverBootID, crashID)
}

func historyUDPExchange(t *testing.T, c client.HyUDPConn, addr string, size int) {
	t.Helper()
	payload := bytes.Repeat([]byte{byte(size % 251)}, size)
	type received struct {
		b    []byte
		addr string
		err  error
	}
	result := make(chan received, 1)
	go func() { b, a, err := c.Receive(); result <- received{b, a, err} }()
	require.NoError(t, c.Send(payload, addr))
	select {
	case r := <-result:
		require.NoError(t, r.err)
		require.Equal(t, addr, r.addr)
		require.Equal(t, payload, r.b)
	case <-time.After(5 * time.Second):
		t.Fatal("UDP reply deadline")
	}
}
