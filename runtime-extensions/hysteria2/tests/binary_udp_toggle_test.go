package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/stretchr/testify/require"
)

type historyUDPTarget struct {
	conn  net.PacketConn
	peers chan net.Addr
}

func historyUDPPeerTarget(t *testing.T) *historyUDPTarget {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	target := &historyUDPTarget{conn: conn, peers: make(chan net.Addr, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			target.peers <- peer
			if _, err := conn.WriteTo(buf[:n], peer); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { conn.Close(); <-done })
	return target
}

func (target *historyUDPTarget) exchange(t *testing.T, c client.HyUDPConn, size int) net.Addr {
	t.Helper()
	historyUDPExchange(t, c, target.conn.LocalAddr().String(), size)
	select {
	case peer := <-target.peers:
		return peer
	case <-time.After(5 * time.Second):
		t.Fatal("UDP peer observation deadline")
		return nil
	}
}

func (target *historyUDPTarget) unsolicited(t *testing.T, c client.HyUDPConn, peer net.Addr, size int) {
	t.Helper()
	type received struct {
		b    []byte
		addr string
		err  error
	}
	reply := make(chan received, 1)
	go func() { b, a, err := c.Receive(); reply <- received{b, a, err} }()
	payload := make([]byte, size)
	_, err := target.conn.WriteTo(payload, peer)
	require.NoError(t, err)
	select {
	case r := <-reply:
		require.NoError(t, r.err)
		require.Equal(t, target.conn.LocalAddr().String(), r.addr)
		require.Equal(t, payload, r.b)
	case <-time.After(5 * time.Second):
		t.Fatal("unsolicited UDP reply deadline")
	}
}

func TestHistoryBinaryUDPToggle(t *testing.T) {
	// Given a real association whose idle timer cannot accidentally provide the
	// cleanup being tested, and all event/packet subscriptions installed first.
	evidence := filepath.Join(os.Getenv("HYSTERIA_EVIDENCE"), "udp-toggle")
	require.NoError(t, os.MkdirAll(evidence, 0700))
	a, b, response := historyUDPPeerTarget(t), historyUDPPeerTarget(t), historyUDPPeerTarget(t)
	p := historyStartBinary(t, evidence, "600s")
	collector := collectHistory(t, p.socket, filepath.Join(evidence, "events.jsonl"))
	hello := collector.hello(t)
	collector.send(t, nodeprov.HistoryResume{Op: "resume", ObserverBootID: hello.ObserverBootID})
	epoch := nodeprov.HistoryUUID("11111111-1111-4111-8111-111111111111")
	collector.apply(t, 1, epoch, true)
	cl := p.client(t, "alice")
	tunnel := collector.matching(t, "tunnel_start", 1, nil)[0]
	udp, err := cl.UDP()
	require.NoError(t, err)
	t.Cleanup(func() { udp.Close() })
	peer := a.exchange(t, udp, 19)
	response.unsolicited(t, udp, peer, 7)
	starts := collector.matching(t, "flow_start", 2, nil)
	oldIDs := map[string]bool{*starts[0].FlowID: true, *starts[1].FlowID: true}
	pid := p.cmd.Process.Pid
	// When the same live outbound UDP socket carries new targets and unsolicited
	// sources while OFF, none can be retained as collection owners.
	off := collector.apply(t, 2, epoch, false)
	require.Equal(t, peer.String(), b.exchange(t, udp, 53).String())
	require.Equal(t, peer.String(), a.exchange(t, udp, 37).String())
	response.unsolicited(t, udp, peer, 41)
	epoch2 := nodeprov.HistoryUUID("22222222-2222-4222-8222-222222222222")
	on := collector.apply(t, 3, epoch2, true)
	collector.mu.Lock()
	enabledFrames := append([]nodeprov.HistoryEvent(nil), collector.events...)
	collector.mu.Unlock()
	for _, e := range enabledFrames {
		if e.Sequence > off.BarrierSequence && e.Sequence <= on.BarrierSequence {
			require.Nil(t, e.FlowID, "OFF destinations must not reappear as enable snapshots")
		}
	}
	require.Equal(t, peer.String(), a.exchange(t, udp, 13).String())
	require.Equal(t, peer.String(), b.exchange(t, udp, 17).String())
	response.unsolicited(t, udp, peer, 11)
	newStarts := collector.matching(t, "flow_start", 5, func(e nodeprov.HistoryEvent) bool { return e.CollectionEpoch == epoch2 })
	require.Len(t, newStarts, 5)
	for _, e := range newStarts {
		require.False(t, oldIDs[*e.FlowID])
		require.Equal(t, *tunnel.TunnelID, *e.TunnelID)
		require.Equal(t, *tunnel.Auth.CredentialRef, *e.Auth.CredentialRef)
		require.Equal(t, hello.ObserverBootID, e.ObserverBootID)
		require.True(t, e.Lifecycle.PartialStart)
		require.Nil(t, e.Lifecycle.StartedAt)
		require.Zero(t, *e.Bytes.Upload)
		require.Zero(t, *e.Bytes.Download)
	}
	require.Equal(t, pid, p.cmd.Process.Pid)
	require.NoError(t, cl.Close())
	ends := collector.matching(t, "flow_end", 5, nil)
	collector.matching(t, "tunnel_end", 1, nil)
	// Then only new partial segments end, with exactly the post-enable bytes.
	require.Len(t, ends, 5)
	var upload, download uint64
	var requested, responseOnly int
	for _, e := range ends {
		require.False(t, oldIDs[*e.FlowID])
		require.True(t, e.Bytes.Final)
		require.True(t, e.Lifecycle.PartialStart)
		upload += uint64(*e.Bytes.Upload)
		download += uint64(*e.Bytes.Download)
		if e.Destination.RequestedHost == nil {
			responseOnly++
			require.Zero(t, *e.Bytes.Upload)
		} else {
			requested++
			require.Zero(t, *e.Bytes.Download)
		}
	}
	require.Equal(t, 2, requested)
	require.Equal(t, 3, responseOnly)
	require.Equal(t, uint64(30), upload)
	require.Equal(t, uint64(41), download)
	collector.mu.Lock()
	events := append([]nodeprov.HistoryEvent(nil), collector.events...)
	collector.mu.Unlock()
	for _, e := range events {
		if oldIDs[valueOrEmpty(e.FlowID)] {
			require.NotEqual(t, "flow_end", e.Kind)
		}
		if e.Sequence > off.BarrierSequence {
			require.NotEqual(t, epoch, e.CollectionEpoch)
		}
	}
	require.NoError(t, collector.file.Sync())
	t.Logf("UDP_TOGGLE_PASS pid=%d boot=%s peer=%s forgotten=2 partial_ends=5 upload=30 download=41", pid, hello.ObserverBootID, peer)
}

func valueOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
