package server

import (
	"bytes"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/stretchr/testify/require"
)

func historyFixture(t *testing.T) (*connectionhistory.Observer, *connectionhistory.Flow) {
	t.Helper()
	o, err := connectionhistory.NewObserver(connectionhistory.Options{
		ProducerID: "hysteria2", Runtime: "hysteria2", RuntimeBuild: "test",
	})
	require.NoError(t, err)
	epoch := nodeprov.HistoryUUID("11111111-1111-4111-8111-111111111111")
	_, err = o.Apply(nodeprov.HistoryApply{Op: "apply", ConfigRevision: 1, CollectionEpoch: &epoch, Enabled: true, Bindings: []nodeprov.HistoryBinding{}})
	require.NoError(t, err)
	tunnel, err := o.NewTunnel(connectionhistory.TunnelInfo{Protocol: "hysteria2", InboundTag: "test", LabelKind: "hysteria_auth_id", Label: "alice"})
	require.NoError(t, err)
	f := historyFlow(o, tunnel, "127.0.0.1:1234", "tcp", false)
	require.NotNil(t, f)
	return o, f
}

func historyEvents(t *testing.T, o *connectionhistory.Observer) []nodeprov.HistoryEvent {
	t.Helper()
	lines, err := o.Resume(nodeprov.HistoryResume{Op: "resume", ObserverBootID: o.BootID()})
	require.NoError(t, err)
	events := make([]nodeprov.HistoryEvent, 0, len(lines))
	for _, line := range lines {
		e, err := nodeprov.DecodeHistoryEvent(line)
		require.NoError(t, err)
		events = append(events, e)
	}
	return events
}

type historyReadWriter struct {
	read    func([]byte) (int, error)
	written bytes.Buffer
}

func (r *historyReadWriter) Read(b []byte) (int, error)  { return r.read(b) }
func (r *historyReadWriter) Write(b []byte) (int, error) { return r.written.Write(b) }

func TestHistoryCopyJoinsPartialError(t *testing.T) {
	// Given a partial upload read with an error and an in-flight download read.
	o, f := historyFixture(t)
	entered, stop, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := &historyReadWriter{read: func(b []byte) (int, error) {
		<-entered
		return copy(b, "upload"), io.ErrUnexpectedEOF
	}}
	remote := &historyReadWriter{read: func(b []byte) (int, error) {
		close(entered)
		<-release
		return copy(b, "late"), io.EOF
	}}
	done := make(chan error, 1)
	// When one copy fails, closure wakes the other but finalization must join it.
	go func() {
		err := copyTwoWayHistory("alice", server, remote, nil, &StreamStats{}, f, func() { close(stop) })
		if closeErr := f.Close(historyEnd(err, "eof")); closeErr != nil {
			done <- closeErr
			return
		}
		done <- err
	}()
	select {
	case <-stop:
	case <-time.After(5 * time.Second):
		t.Fatal("copy did not request shutdown")
	}
	select {
	case err := <-done:
		t.Fatalf("returned before opposite read quiesced: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	case <-time.After(5 * time.Second):
		t.Fatal("copy did not join")
	}
	// Then the late payload is included and no mutation can follow the end.
	events := historyEvents(t, o)
	last := events[len(events)-1]
	require.Equal(t, "flow_end", last.Kind)
	require.True(t, last.Bytes.Final)
	require.Equal(t, nodeprov.HistoryU64(6), *last.Bytes.Upload)
	require.Equal(t, nodeprov.HistoryU64(4), *last.Bytes.Download)
	require.ErrorIs(t, f.Observe(1, 0), connectionhistory.ErrClosed)
}

type historyDenyLogger struct{ upload atomic.Uint64 }

func (l *historyDenyLogger) LogTraffic(_ string, up, down uint64) bool {
	l.upload.Add(up)
	return up == 0
}
func (*historyDenyLogger) LogOnlineState(string, bool)        {}
func (*historyDenyLogger) TraceStream(HyStream, *StreamStats) {}
func (*historyDenyLogger) UntraceStream(HyStream)             {}

func TestHistoryCopyPreservesEnforcement(t *testing.T) {
	// Given a logger rejecting an observed upload before the outbound write.
	o, f := historyFixture(t)
	stop := make(chan struct{})
	server := &historyReadWriter{read: func(b []byte) (int, error) { return copy(b, "denied"), nil }}
	remote := &historyReadWriter{read: func([]byte) (int, error) { <-stop; return 0, io.EOF }}
	logger := &historyDenyLogger{}
	// When the history-enabled forwarding boundary reads the payload.
	err := copyTwoWayHistory("alice", server, remote, logger, &StreamStats{}, f, func() { close(stop) })
	// Then the enforcement result and old aggregate meaning remain intact.
	require.True(t, errors.Is(err, errDisconnect))
	require.Equal(t, uint64(6), logger.upload.Load())
	require.Zero(t, remote.written.Len())
	require.NoError(t, f.Close(historyEnd(err, "eof")))
	events := historyEvents(t, o)
	last := events[len(events)-1]
	require.Equal(t, nodeprov.HistoryU64(6), *last.Bytes.Upload)
	require.Equal(t, "policy_reject", *last.Lifecycle.EndReason)
}
